# Quickstart: Validate Preserve Stock Move Schema

## Prerequisites

- Go 1.25 and dependencies installed (`go mod download`).
- `DATABASE_URL`, `APP_ENV`, and `API_JWT_SECRET` from `.env.example`.
- Optional but recommended: a disposable PostgreSQL database set via `TEST_DATABASE_URL`
  for the repository integration tests.

## Validation scenarios

### 1. Schema source-of-truth check

Confirm the canonical schema has no `reservation_id` on the stock-movement table:

```bash
grep -n "reservation_id" db.dbml db.dbdiagram || echo "OK: no reservation_id in schema artifacts"
```

Expected: `OK: no reservation_id in schema artifacts`.

### 2. Migration check

Confirm no migration adds or requires a `reservation_id` column on `stock_moves`:

```bash
grep -n "reservation_id" migrations/*.sql || echo "OK: no reservation_id in migrations"
```

Also confirm no migration alters the stock-movement table in a way that would add the
column. Expected: the migration set contains no `ALTER TABLE stock_moves ... reservation_id`.

### 3. Code reference sweep

Confirm no repository, model, or domain code references a `reservation_id` on the
stock-movement table:

```bash
grep -rn "reservation_id" internal/ || echo "OK: no reservation_id in internal/"
```

Expected: the only matches are the schema-preservation guard assertions in
`internal/infrastructure/repository/inventoryRepo_test.go`, which name the forbidden
string in order to reject it. No production code path inserts, reads, or references a
`reservation_id` column. (The reservation identifier appears in the Go code as
`ReservationID` on the reservation entity and result only — never as a `stock_moves`
column.)

### 4. Documentation sweep

Confirm no plan/spec/task document instructs inserting a `reservation_id` into the
stock-movement table:

```bash
grep -rn "reservation_id" specs/001-checkout-inventory-reservation specs/002-order-owned-expiry || echo "OK: no reservation_id in numbered spec docs"
```

Expected: no matches in the numbered feature docs.

### 5. Behavior check (optional, needs a disposable database)

Create a checkout reservation and confirm the `RESERVE` movement has no reservation
identifier column or value:

```bash
export TEST_DATABASE_URL="$DATABASE_URL"
psql "$TEST_DATABASE_URL" -f migrations/0005_checkout_reservation_integrity.sql
go test ./internal/infrastructure/repository/... -count=1 -run TestInventoryRepo_CreateReservation
```

Then inspect a `RESERVE` row:

```sql
SELECT column_name
FROM information_schema.columns
WHERE table_name = 'stock_moves';
-- Expected: no row named reservation_id
```

### 6. Full suite

```bash
go test ./...
go vet ./...
git diff --check
```

All must pass. The repository integration tests skip automatically when
`TEST_DATABASE_URL` is unset, so `go test ./...` stays green without a database.

## Expected outcomes

- Schema artifacts, migrations, and numbered feature docs contain no `stock_moves`
  `reservation_id` reference.
- The reservation endpoint and its RESERVE stock-movement audit trail behave exactly as
  before the cleanup.
