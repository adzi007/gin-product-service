# Task: Implement Inventory & Reservation Service

**Spec:** [specs/inventory-reservation.md](specs/inventory-reservation.md) — read the full spec before starting. This issue breaks it into ordered, checkable steps that follow this repo's existing Clean Architecture conventions (see `CLAUDE.md`).

## Before you start

- Read `CLAUDE.md` in the repo root — it documents the layering (`delivery/http → app/<module> → domain ← infrastructure`) and module-wiring conventions. Every step below must follow it.
- Look at the `category` module (`internal/domain/category.go`, `internal/app/category/*.go`, `internal/infrastructure/repository/categoryRepo.go`, `internal/delivery/http/handler/category_handler.go`) as the reference pattern for interfaces, use cases, repos, and handlers.
- Some scaffolding already exists — do not recreate it, extend it:
  - `internal/domain/inventory.go` already defines `StockMoveType`, `Quantity` (non-negative int wrapper with `NewQuantity`), `InventoryItem`, `InventoryLevel` (with a `Reserve` method), and `StockMove`. There is no `Reservation` type yet, and no repository/use-case interfaces.
  - `internal/infrastructure/repository/model/inventory_model.go` has a DB model + `ToDomain()` for `InventoryLevel` only.
  - `migrations/0002_add_inventory_levels_unique.sql` already adds a unique index on `(inventory_item_id, location_id)`. The base tables (`inventory_items`, `locations`, `inventory_levels`, `stock_moves`, `reservations`) are assumed to already exist in the database per the spec's schema (Section 2) — verify this against the actual DB before writing migrations for anything new (e.g. constraints, indexes needed for idempotency).
  - `internal/domain/product.go` references `StockMoves []StockMove` on some struct — check for accidental coupling before adding new fields there.

Work through the steps **in order**. Each step should be a separate commit. Run `go build ./...` and `go test ./...` after each step before moving to the next.

---

## Step 0 — Verify current DB schema

1. Connect to the dev database (see `.env` / `DATABASE_URL`) and confirm the tables in spec Section 2 (`inventory_items`, `locations`, `inventory_levels`, `stock_moves`, `reservations`) exist with the columns listed there. If any are missing, write a migration under `migrations/` to create them (idempotent, `IF NOT EXISTS`, following the style of the existing two migration files).
2. Add the `CHECK` constraints from spec Section 23 (`available_qty >= 0`, `reserved_qty >= 0`, `quantity > 0`) if not already present, via a new migration.
3. Add a unique constraint/index to support idempotent reservation creation (spec Section 26) — e.g. a unique index on `reservations(order_id)` if the business rule is "one reservation batch per order", or an `idempotency_keys` table if you want a generic key. Pick the simplest option that satisfies "same order_id retried must not double-reserve" and document the choice in the migration file comment.

**Do not proceed past this step with assumptions about column names** — read the actual schema.

## Step 1 — Domain layer (`internal/domain/`)

Extend `internal/domain/inventory.go` (or add a new `internal/domain/reservation.go` if that keeps the file readable):

1. Add `ReservationStatus` type with `ACTIVE`, `COMPLETED`, `CANCELLED`, `EXPIRED` constants (spec 2.5).
2. Add a `Reservation` struct matching the `reservations` table (spec 2.5): `ID`, `InventoryItemID`, `LocationID`, `OrderID`, `Quantity`, `ReservedAt`, `ExpiresAt`, `Status`, `ReleasedAt`.
3. Add a `Location` struct matching spec 2.2, if not present.
4. Add domain errors: `ErrVariantNotFound`, `ErrInventoryItemNotFound`, `ErrLocationNotFound`, `ErrInventoryLevelNotFound`, `ErrReservationNotFound`, `ErrReservationAlreadyCompleted`, `ErrReservationAlreadyCancelled`, `ErrDuplicateActiveReservation`, `ErrFromToLocationSame` — these map to the HTTP status table in spec Section 21 (404/409/400). Reuse `ErrInsufficientStock` and `ErrInvalidQuantity` already defined.
5. Add request/response input structs for each of the 4 APIs, mirroring the JSON shapes in spec Sections 4.1, 8, 12, 15 (e.g. `CreateStockMoveInput`, `CreateReservationInput`, `ReservationItemInput`).
6. Define the use case interfaces (one per API, following the `InsertCategoryUseCase`/`QueryCategoryUseCase` split pattern):
   - `StockMoveUseCase` — `Create(ctx, CreateStockMoveInput) (StockMove, error)`
   - `ReservationUseCase` — `Create(ctx, CreateReservationInput) (order-level result, error)`, `Complete(ctx, orderID uuid.UUID) (result, error)`, `Cancel(ctx, orderID uuid.UUID) (result, error)`
   - Optionally `ReservationExpiryUseCase` — `ExpireDue(ctx) (int, error)` for Step 5's background worker.
7. Define the repository interface(s) the use cases depend on, e.g. `InventoryRepository`:
   - `ResolveInventoryItemIDByVariant(ctx, variantID) (uuid.UUID, error)`
   - `LockInventoryLevel(ctx, tx, inventoryItemID, locationID) (InventoryLevel, error)` — must be callable within a caller-supplied transaction (see Step 3 on how this repo does transactions; if there's no existing transaction abstraction, add one — check `internal/infrastructure/database/database.go` for what's there before inventing a new pattern).
   - `UpdateInventoryLevel(ctx, tx, level InventoryLevel) error`
   - `InsertStockMove(ctx, tx, move StockMove) error`
   - `InsertReservation(ctx, tx, r Reservation) error`
   - `FindActiveReservationsByOrderID(ctx, tx, orderID) ([]Reservation, error)`
   - `FindReservationsByOrderID(ctx, tx, orderID) ([]Reservation, error)` (for idempotent complete/cancel checks)
   - `UpdateReservationStatus(ctx, tx, reservationID, status, releasedAt) error`
   - `FindDueActiveReservations(ctx, tx, now) ([]Reservation, error)` (for expiry)

Keep interfaces minimal — add methods as the use cases in Step 3 actually need them, don't speculate beyond the spec.

## Step 2 — Transaction support

Check `internal/infrastructure/database/database.go` and `postgres.go` for whether a `BeginTx`/transaction-scoped executor already exists (the `Database` interface wraps `*pgxpool.Pool`).

- If it doesn't, add a minimal way for a repository method to run multiple statements in one `pgx.Tx` — e.g. a `Database.WithTx(ctx, func(tx pgx.Tx) error) error` helper, or expose `Pool().Begin(ctx)` and let the repository manage `tx.Commit()`/`tx.Rollback()`.
- This is required by spec Section 3.2 — every multi-statement operation (stock move, reservation create/complete/cancel) must be one transaction.
- All locking (`SELECT ... FOR UPDATE`) must happen using the transaction handle, not the pool directly, or the lock is meaningless.

## Step 3 — Repository layer (`internal/infrastructure/repository/inventoryRepo.go`)

Follow `categoryRepo.go` / `productRepo.go` conventions: `pgx.CollectRows` + `pgx.RowToStructByName`, and wrap every method with `metrics.ObserveDB("inventory", "<operation>")(time.Now())`.

Implement each interface method from Step 1. Key correctness points from the spec — do not skip these:

- **Resolve variant → inventory_item_id** internally (spec 3.1) — never accept `inventory_item_id` from a handler input.
- **Row locking** (spec 3.3): every read-before-write on `inventory_levels` uses `SELECT ... FOR UPDATE`.
- **Deterministic lock ordering** (spec 22): when a single operation locks multiple `inventory_levels` rows (TRANSFER between two locations, multi-item reservation), sort the target rows by `(inventory_item_id, location_id)` before acquiring locks, to avoid deadlocks.
- Add corresponding DB models in `internal/infrastructure/repository/model/inventory_model.go` (e.g. `Reservation`, `Location`, `StockMove` DB structs with `db:"..."` tags and `ToDomain()` methods) — extend the existing file, don't duplicate `InventoryLevel`.

## Step 4 — Use case layer (`internal/app/inventory/`)

Create one file per operation (matches `internal/app/category/insertUseCase.go` style):

1. `stockMoveUseCase.go` — implements spec Sections 4–6:
   - Validate per move type (4.2): `IN` needs `to_location_id`, `OUT` needs `from_location_id` + sufficient stock, `TRANSFER` needs both + different + sufficient stock at source, `ADJUST` computes `difference = requested - current` and sets `available_qty` to the requested value directly.
   - Generate the `StockMove.ID` as UUIDv7 here (per `CLAUDE.md` convention), not in the repo.
   - Wrap the whole operation in one transaction (Step 2's helper).
2. `reservationUseCase.go`:
   - `Create` — implements spec Sections 9–11: validate all items up front, then for every item in a single transaction: resolve inventory item, lock level (in deterministic order across all items first — see spec 22), check `available_qty >= quantity`, decrement available/increment reserved, insert `Reservation` row (UUIDv7 IDs). If any item fails, the whole transaction rolls back — no partial reservations (spec 9/10). Handle the idempotency requirement (spec 9, 26): if an active reservation set already exists for `order_id`, return the existing result instead of creating a duplicate — decide and implement based on the unique constraint added in Step 0.3.
   - `Complete` — implements spec Sections 12–14: find `ACTIVE` reservations for `order_id`, lock reservation + inventory level rows, decrement `reserved_qty` only (never touch `available_qty`), set `status = COMPLETED`, `released_at = now()`. If no active reservations exist, check whether the order was already completed (return success, idempotent) vs. never existed (404/409). If any single item is missing from the query, that's fine — the query only returns what's ACTIVE.
   - `Cancel` — implements spec Sections 15–17: same lock pattern, but restores `available_qty += quantity` as well as decrementing `reserved_qty`, sets `status = CANCELLED`.
   - Idempotency for `Complete`/`Cancel`: calling twice on an already-`COMPLETED`/`CANCELLED` order must be a no-op returning the current state, not an error and not a double-mutation (spec 12, 13, 15, 16).
3. (Optional, spec Section 18) `reservationExpiryUseCase.go` — `ExpireDue`: find `ACTIVE` reservations where `expires_at < now()`, apply the same state transition as `Cancel` (restore available, decrement reserved, `status = EXPIRED`). This will be invoked by a scheduled job in Step 6, not by an HTTP handler.

Constructors return domain interface types (`domain.ReservationUseCase`, etc.), not concrete structs — match `NewCategoryInsertUseCase(...) domain.InsertCategoryUseCase`.

## Step 5 — HTTP delivery layer

1. Add DTOs in `internal/delivery/http/dto/inventory_dto.go` for request/response bodies (spec 4.1/6, 8/11, 14, 17) — separate from domain types per this repo's existing pattern (see `product_dto.go`).
2. Add `internal/delivery/http/handler/inventory_handler.go` with:
   - `POST /v1/inventory/stock-moves`
   - `POST /v1/inventory/reservations`
   - `PUT /v1/inventory/reservations/:orderId/complete`
   - `PUT /v1/inventory/reservations/:orderId/cancel`
3. Map domain errors to HTTP status codes per spec Section 21 (400/404/409/500). Check how `category_handler.go` / `product_handler.go` currently map errors (likely a shared error-to-status helper) and reuse it rather than inventing a new pattern.
4. Register the routes in `internal/delivery/http/router.go` under `/api/v1` (check whether the spec's literal paths `/v1/inventory/...` should be nested under the existing `/api/v1` group or added as-is — match whatever convention the existing routes use, note the discrepancy if the spec's paths don't already start with `/api`).
5. Wire the new handler into `internal/wire/container.go` (repo → use cases → handler, following the `category`/`product` blocks already there) and thread it through `cmd/server/gin_server.go`'s `SetupRouter` call.
6. Add Swagger annotations matching the existing handlers' style, then regenerate docs: `swag init -g cmd/main.go -o docs --parseInternal` (run from repo root).

## Step 6 — Reservation expiration worker (spec Section 18)

Implement as a background goroutine or scheduled ticker (check `cmd/main.go` / `cmd/server/gin_server.go` for how the server starts background work, if anything, before inventing a new pattern) that periodically calls `ReservationExpiryUseCase.ExpireDue`. Make sure it shuts down cleanly with the existing SIGINT/SIGTERM graceful shutdown (`gin_server.go` already has a 15s shutdown timeout — the worker must respect the same shutdown signal, not leak a goroutine).

## Step 7 — Tests (spec Section 27 items 13–15, Section 28)

Write these as you go per layer, not all at the end:

- **Use case unit tests** (`internal/app/inventory/*_test.go`), mocking the repository interface: cover every scenario in spec Section 28 — IN, OUT, OUT-insufficient, TRANSFER, ADJUST increase/decrease, multi-item reservation success, one-item-insufficient (full rollback, no partial state), completion (incl. double-completion no-op), cancellation (incl. double-cancellation no-op).
- **Repository/integration tests** for transaction and locking behavior — needs a real or test Postgres instance (check if `productRepo_test.go` already sets up a test DB connection/container; reuse that setup). Must include the concurrent-reservation scenario from spec Section 28: two goroutines racing to reserve more stock than available combined — assert exactly one succeeds and final `available_qty` never goes negative.
- **HTTP handler tests** (`internal/delivery/http/handler/inventory_handler_test.go`) for success and each error status (400/404/409), following `product_handler_test.go`'s pattern (likely uses `httptest` + a mocked use case).

Run `go test ./...` and confirm everything passes before considering the task done.

## Definition of done

- [ ] Schema verified/migrated (Step 0)
- [ ] Domain types, errors, and interfaces added (Step 1)
- [ ] Transaction support in place (Step 2)
- [ ] Repository implemented with row locking + deterministic lock ordering (Step 3)
- [ ] All 4 use cases implemented and idempotent where required (Step 4)
- [ ] All 4 endpoints wired end-to-end through `router.go` and `wire/container.go` (Step 5)
- [ ] Expiration worker implemented and shuts down cleanly (Step 6)
- [ ] Unit, repository, and handler tests all passing, covering every scenario in spec Section 28 (Step 7)
- [ ] `go build ./...` and `go test ./...` pass with no failures
- [ ] Swagger docs regenerated
