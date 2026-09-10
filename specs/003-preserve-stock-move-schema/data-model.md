# Data Model: Preserve Stock Move Schema

This feature makes no schema change. It pins the canonical `stock_moves` shape and
records the explicit non-requirement that the table must not carry a reservation
identifier.

## StockMove

The stock-movement table retains exactly its canonical columns.

| Field | Type | Rules |
|---|---|---|
| `id` | UUID | Primary key; UUIDv7 for rows written by the reservation path. |
| `inventory_item_id` | UUID | Required FK to the inventory item; resolved internally from a public variant identifier. |
| `from_location_id` | UUID | Nullable FK to a location; source for `OUT`/`TRANSFER`/`RESERVE` semantics. |
| `to_location_id` | UUID | Nullable FK to a location; destination for `IN`/`TRANSFER` semantics. |
| `move_type` | `stock_move_type` | Required; one of `IN`, `OUT`, `TRANSFER`, `ADJUST`, `RESERVE`, `UNRESERVE`. |
| `quantity` | integer | Quantity moved; whole number. |
| `created_by` | UUID | Nullable actor identifier. |
| `reason` | text | Nullable free-text reason. |
| `created_at` | `timestamptz` | Creation time, defaulting to `now()`. |

**Explicit non-column**: `reservation_id` MUST NOT exist on `stock_moves`. No code path
may insert, update, select, or reference it.

## Reservation (unchanged)

Reservation identity and order association remain solely on the `reservations` table,
which already carries the reservation identifier, inventory item, location, order, quantity,
reservation time, expiry, status, and release time. This feature does not modify it.

## Relationships

- A stock movement references an `inventory_item` (and optionally locations) only.
- A stock movement has no direct reference to a reservation. A `RESERVE` movement records
  the same inventory item, location, and quantity as its corresponding reservation, but the
  reservation identifier is not duplicated onto the movement.
- The reservation remains the entity that ties a held quantity to an order; the movement
  remains the entity that records that quantity changed location/reservation state.

## Mapping notes

The existing `RESERVE` movement write uses only `id`, `inventory_item_id`,
`from_location_id`, `to_location_id`, `move_type`, and `quantity`, leaving `created_by`
and `reason` null and `created_at` to its database default. This shape is the target state
and must not change.
