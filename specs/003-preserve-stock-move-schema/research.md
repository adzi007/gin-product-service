# Phase 0 Research: Preserve Stock Move Schema

## Decision: `stock_moves` keeps its canonical nine-column shape with no `reservation_id`

- **Decision**: Treat the user-supplied `stock_moves` schema as authoritative. The table
  remains exactly `id`, `inventory_item_id`, `from_location_id`, `to_location_id`,
  `move_type`, `quantity`, `created_by`, `reason`, `created_at` — no `reservation_id`.
- **Rationale**: A stock movement is an audit record of a quantity/location change.
  Attaching the reservation identifier to every movement duplicates identity that the
  `reservations` table already owns, and the two records can drift apart.
- **Alternatives considered**: Adding a nullable `reservation_id` to `stock_moves`
  (rejected — redundant identity and schema drift from the canonical definition).

## Decision: Reservation identity lives only on `reservations`

- **Decision**: The `reservations` table remains the sole durable holder of reservation
  identity and order association. A `RESERVE` stock movement links back to the reservation
  only indirectly, through the inventory item, location, movement type, and quantity.
- **Rationale**: This matches the service's existing domain model and the current
  `domain.StockMove` / `model.StockMove` shapes, which carry no reservation identifier.
- **Alternatives considered**: Making the movement the join point for reservation lookup
  (rejected — reverse lookup stays on `reservations`).

## Decision: No new migration; sweep existing DDL instead

- **Decision**: Do not add a migration. Audit `db.dbml`, `db.dbdiagram`, and every file
  under `migrations/` and remove or correct any statement that would add or require a
  `reservation_id` column on `stock_moves`.
- **Rationale**: The current migration set already avoids altering `stock_moves`
  (migration 0005 preserves it, and an existing test asserts this). A cleanup should not
  introduce a schema change to undo a schema change that does not exist.
- **Alternatives considered**: A corrective `DROP COLUMN` migration (rejected — only
  needed if a prior migration had actually added the column).

## Decision: Behavior-preserving; no API contract change

- **Decision**: The reservation endpoint, its request/response shapes, and all
  inventory/reservation invariants are unchanged. The feature only removes references to
  a non-existent column.
- **Rationale**: FR-008 requires no change to the externally observable reservation
  contract. The RESERVE stock movement write continues to use the existing columns.
- **Alternatives considered**: Refactoring the RESERVE insert or the movement model
  (rejected — unnecessary and would risk behavior change).

## Decision: Scope excludes reservation response/identity references

- **Decision**: References to `reservation_id` as a reservation identifier in a response
  payload, or `reservationId` in the API contract, are out of scope. Only references to a
  `reservation_id` column on `stock_moves` are in scope.
- **Rationale**: The request specifically targets the `stock_moves` table's
  `reservation_id` column. Removing the reservation record's own identity field would
  break the reservation contract.
- **Alternatives considered**: Broad deletion of every `reservation_id` string in the
  repository (rejected — would destroy legitimate reservation identity fields).

## Sources consulted

- [Feature specification](spec.md): required schema conformance and scope boundaries.
- `db.dbml` and `db.dbdiagram`: current `stock_moves` definition.
- `migrations/0005_checkout_reservation_integrity.sql` and
  `migrations/0006_order_owned_reservation_expiry.sql`: confirm no `stock_moves` alteration.
- `internal/domain/inventory.go`, `internal/infrastructure/repository/model/reservation_model.go`,
  and `internal/infrastructure/repository/inventoryRepo.go`: confirm the movement insert and
  mapping use existing columns only.
- `specs/001-checkout-inventory-reservation/` and `specs/002-order-owned-expiry/`: confirm
  the numbered plans already state "using the existing `stock_moves` columns".
