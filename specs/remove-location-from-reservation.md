# Remove `location_id` from Reservation Creation

## 1. Goal

`POST /v1/inventory/reservations` currently requires the caller (Order Service)
to pass a `location_id` for every item:

```json
{
  "order_id": "uuid",
  "expires_at": "2026-08-25T01:00:00Z",
  "items": [
    { "variant_id": "uuid", "location_id": "uuid", "quantity": 2 }
  ]
}
```

After this change, `location_id` is no longer part of the request. The
reservation flow resolves `variant_id` → `inventory_item_id` and then decides
**which location to reserve from itself**, by reading `inventory_levels`.

```json
{
  "order_id": "uuid",
  "expires_at": "2026-08-25T01:00:00Z",
  "items": [
    { "variant_id": "uuid", "quantity": 2 }
  ]
}
```

The response is unchanged: each reservation row in the response still reports
the `location_id` that was actually used, since that is now decided by the
server instead of the caller.

Only **reservation creation** changes. Stock moves
(`POST /inventory/stock-moves`) keep requiring explicit `from_location_id` /
`to_location_id` — do not touch that endpoint.

## 2. Design decision: single-location fulfillment

An `inventory_item` can have more than one `inventory_levels` row (one per
location). Since the caller no longer says which one to use, this
implementation picks **exactly one** location per item and requires that
single location to cover the whole requested quantity. If no single location
has enough stock, the reservation fails with `ErrInsufficientStock` — even if
the sum across multiple locations would technically be enough. Splitting one
reservation line across multiple locations is out of scope for this change.

Selection rule, most preferred first:

1. The location flagged `is_default = true` in the `locations` table, **if**
   it has enough `available_qty` for the requested quantity.
2. Otherwise, the location with the highest `available_qty` for that
   inventory item.
3. Tie-break by `location_id ASC` so the choice is deterministic (needed for
   the lock-ordering rule in spec Section 22 of `inventory-reservation.md`).

If the item has zero `inventory_levels` rows at all, this is
`ErrInventoryLevelNotFound` (same as today).

This selection must happen as part of the same `SELECT ... FOR UPDATE` that
locks the row — do not read levels un-locked to "decide" a location and then
lock separately, since that reintroduces the race the locking is meant to
prevent.

## 3. Step-by-step implementation

### 3.1 Domain layer — `internal/domain/reservation.go`

- Remove `LocationID` from `ReservationItemInput` (line ~64). The struct
  becomes:

  ```go
  type ReservationItemInput struct {
      VariantID uuid.UUID `json:"variant_id"`
      Quantity  int       `json:"quantity"`
  }
  ```

- In `validateReservationInput` — actually this function lives in
  `internal/app/inventory/reservationUseCase.go` (see 3.3), not this file;
  just make sure you remove the `item.LocationID == uuid.Nil` check there
  too.
- `Reservation` struct (the persisted/returned row) keeps its `LocationID`
  field unchanged — it's still needed, it's just populated by the server
  instead of echoed from the request.

### 3.2 Domain layer — `internal/domain/inventory.go`

Replace the `LockInventoryLevel` method on `InventoryRepository` (used today
by reservation create/complete/cancel) with a version that doesn't take
`locationID` for the **create** path. Complete/Cancel/Expire still know the
`location_id` from the existing reservation row, so they keep using a
location-scoped lock.

Add a new method instead of changing the old one's signature, so the
existing complete/cancel code paths are untouched:

```go
// LockInventoryLevelByItem locks and returns the single inventory level to
// reserve from for an item when no location was supplied by the caller
// (spec: remove-location-from-reservation.md). Selection prefers the
// is_default location if it has enough stock, otherwise the location with
// the highest available_qty, tie-broken by location_id ASC. Returns
// ErrInventoryLevelNotFound when the item has no inventory_levels rows.
LockInventoryLevelByItem(ctx context.Context, tx Tx, inventoryItemID uuid.UUID, requiredQty Quantity) (InventoryLevel, error)
```

Keep the existing `LockInventoryLevel(ctx, tx, inventoryItemID, locationID)`
method as-is — it's still used by `Complete`/`Cancel`/expiry, which already
know the location from the reservation row.

Also delete the now-unused `LocationExists` method from the interface **only
if** nothing else calls it after step 3.3 — check `stockMoveUseCase.go`
first, since stock moves still validate `from_location_id`/`to_location_id`
and likely still need it. If stock moves still use it, leave it in place.

### 3.3 Application layer — `internal/app/inventory/reservationUseCase.go`

In `Create` (~line 30):

- Delete the `LocationExists` check inside the "Pass 1a" loop (~lines 73-79).
- Change the `resolvedItem` struct to drop `locationID` — you don't have one
  yet at this point.
- Change "Pass 1b" so that locking calls `repo.LockInventoryLevelByItem(ctx,
  tx, r.itemID, domain.Quantity(r.quantity))` instead of
  `repo.LockInventoryLevel(ctx, tx, r.itemID, r.locationID)`. The returned
  `InventoryLevel.LocationID` is the location the server picked — use it to
  fill `reservation.LocationID` when building each `lockedItem`.
- Sorting before locking (~lines 91-96) only needs to sort by `itemID` now
  (there's one lock call per item, keyed only by `inventory_item_id`, so
  there's no `locationID` to break ties with yet at lock time).
- The availability check in "Pass 2" (~lines 125-129) stays the same — it's
  already comparing `level.AvailableQty` against `reservation.Quantity`;
  `LockInventoryLevelByItem` should raise `ErrInsufficientStock` itself if no
  single location qualifies (see 3.4), so this pass becomes a defensive
  double-check, not the primary gate. Keep it — it's cheap and still correct.

In `validateReservationInput` (~line 297): remove the
`item.LocationID == uuid.Nil` clause from the per-item check.

`Complete` and `Cancel` are unaffected — they already read `LocationID` off
the stored `Reservation` row (set at creation time), not from a request.

### 3.4 Infrastructure layer — `internal/infrastructure/repository/inventoryRepo.go`

Add `LockInventoryLevelByItem`, implemented as one query that both selects
the best location and locks it:

```go
func (r *inventoryRepo) LockInventoryLevelByItem(ctx context.Context, tx pgx.Tx, inventoryItemID uuid.UUID, requiredQty domain.Quantity) (domain.InventoryLevel, error) {
	defer metrics.ObserveDB("inventory", "lock_inventory_level_by_item")(time.Now())

	rows, err := tx.Query(ctx, `
		SELECT
			il.id,
			il.inventory_item_id,
			il.location_id,
			COALESCE(il.available_qty, 0) AS available_qty,
			COALESCE(il.reserved_qty, 0) AS reserved_qty,
			COALESCE(il.updated_at, now()) AS updated_at
		FROM inventory_levels il
		JOIN locations l ON l.id = il.location_id
		WHERE il.inventory_item_id = $1
		ORDER BY
			(l.is_default AND il.available_qty >= $2) DESC,
			il.available_qty DESC,
			il.location_id ASC
		LIMIT 1
		FOR UPDATE OF il`,
		pgUUID(inventoryItemID),
		requiredQty.Int(),
	)
	if err != nil {
		return domain.InventoryLevel{}, err
	}
	defer rows.Close()

	level, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.InventoryLevel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryLevel{}, domain.ErrInventoryLevelNotFound
		}
		return domain.InventoryLevel{}, err
	}
	return level.ToDomain(), nil
}
```

Notes:

- `FOR UPDATE OF il` combined with `ORDER BY ... LIMIT 1` locks only the one
  row that was picked, not every level row for the item — that's
  intentional and matches "single-location fulfillment".
- The existing `LockInventoryLevel` method currently has its
  `WHERE inventory_item_id = $1 AND location_id = $2` clause with the
  `location_id` filter commented out (see the `// pgUUID(locationID)` line).
  That looks like leftover WIP from a previous change and is a latent bug:
  today, if an item ever has more than one `inventory_levels` row,
  `LockInventoryLevel` will fail because `pgx.CollectOneRow` errors on more
  than one row. Fix that while you're in this file by restoring the
  `AND location_id = $2` filter (with `pgUUID(locationID)` uncommented) —
  `LockInventoryLevel` is still used by `Complete`/`Cancel`/expiry where the
  location **is** known, so it should filter on it.
- This does not need to check `available_qty >= requiredQty` in the `WHERE`
  clause — insufficient stock should still return `ErrInsufficientStock` from
  the use case's Pass 2 check (see 3.3), not silently fall through to "no
  rows found" (which would incorrectly map to `ErrInventoryLevelNotFound`,
  a 404, instead of `ErrInsufficientStock`, a 409).

### 3.5 DTO layer — `internal/delivery/http/dto/inventory_dto.go`

- Remove `LocationID` from `ReservationItemRequest` (~line 89-93):

  ```go
  type ReservationItemRequest struct {
      VariantID uuid.UUID `json:"variant_id" binding:"required" validate:"required"`
      Quantity  int       `json:"quantity" binding:"required" validate:"required,gt=0"`
  }
  ```

- Update `ToDomain()` (~line 95-101) to stop setting `LocationID`.
- `ReservationData` (response, ~line 149) and `ToReservationData` are
  unchanged — they still report `LocationID` from the domain `Reservation`,
  which is now server-assigned rather than caller-supplied.

### 3.6 Handler layer — `internal/delivery/http/handler/inventory_handler.go`

No code changes needed. Update the `@Description` swagger comment on
`CreateReservation` (~line 91) to mention that the location is chosen
automatically, then regenerate docs:

```bash
swag init -g cmd/main.go -o docs --parseInternal
```

### 3.7 Tests to update

- `internal/app/inventory/fake_repo_test.go`: add a
  `LockInventoryLevelByItem` method to `fakeInventoryRepo` that mimics the
  selection rule (prefer default location with enough stock, else highest
  `available_qty`, tie-break by `LocationID`) over its in-memory `f.levels`
  map. Keep the existing `LockInventoryLevel` fake as-is for
  complete/cancel.
- `internal/app/inventory/reservationUseCase_test.go`: remove `LocationID`
  from test request fixtures; add a case covering the new selection rule
  (e.g. two locations for the same item, assert the reservation picks the
  one with more stock / the default one).
- `internal/app/inventory/integration_test.go`: same fixture updates if it
  builds `CreateReservationInput`/`ReservationItemInput` literals directly.
- `internal/delivery/http/handler/inventory_handler_test.go`: remove
  `location_id` from reservation request JSON fixtures; assert the response
  still contains a `location_id` per reservation.

### 3.8 Spec cross-reference

`specs/inventory-reservation.md` Sections 8-11 still document the old
request shape with `location_id` per item. Once this change lands, update
those sections (Section 8 request example, Section 9 per-item validation
list) to match — do not leave the master spec contradicting the actual API.

## 4. Acceptance criteria

- `POST /inventory/reservations` accepts requests without `location_id` in
  `items[]` and rejects the field being required (it's fine if callers still
  send it and it's silently ignored via `ShouldBindJSON`, but it must not be
  `binding:"required"` anywhere).
- When an item's inventory is entirely in one location, that location is
  used, matching current behavior.
- When an item has multiple `inventory_levels` rows and the default location
  has enough stock, the default location is used even if another location
  has more stock.
- When an item has multiple `inventory_levels` rows, no default (or the
  default lacks enough stock), and one non-default location has enough
  stock, that location (highest `available_qty`) is used.
- When no single location has enough stock (even though the sum across
  locations would), the reservation fails with `ERR_INSUFFICIENT_STOCK`
  (409), and nothing is committed for the whole batch (existing
  all-or-nothing behavior, spec Section 7, is preserved).
- Concurrent reservations against the same item/location still serialize
  correctly (`SELECT ... FOR UPDATE`) — no double-reservation, no negative
  `available_qty`.
- `Complete`, `Cancel`, and the expiry worker are unaffected: they still use
  the `location_id` stored on the reservation row from creation time.
- All existing tests pass; new tests cover the location-selection rule
  described in Section 2.
