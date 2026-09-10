# Quickstart: Validate Checkout Inventory Reservation

## Prerequisites

- Go 1.25 and dependencies installed with `go mod download`.
- A disposable PostgreSQL database containing the base [PRD schema](../../PRD.md), a
  single default location, tracked active variants, inventory items, and levels.
- A reachable Upstash Redis REST database. Set `REDIS_REST_URL` and
  `REDIS_REST_TOKEN`; never print either value.
- `DATABASE_URL`, `APP_ENV`, and `API_JWT_SECRET` from `.env.example`.

Apply incremental and new checkout migrations to the disposable database:

```bash
psql "$DATABASE_URL" -f migrations/0001_add_variant_position.sql
psql "$DATABASE_URL" -f migrations/0002_add_inventory_levels_unique.sql
psql "$DATABASE_URL" -f migrations/0005_checkout_reservation_integrity.sql
go test ./...
```

## Success and idempotency

Run the service:

```bash
go run cmd/main.go
```

Submit prepared order/variant UUIDs; item IDs are product variant IDs only:

```bash
curl --fail-with-body -X POST http://localhost:5000/api/v1/inventory/reservations \
  -H 'Content-Type: application/json' \
  -d '{
    "orderId":"11111111-1111-4111-8111-111111111111",
    "items":[{"id":"22222222-2222-4222-8222-222222222222","qty":2}]
  }'
```

Expected: HTTP 201, `status: success`, an `ACTIVE` reservation, and expiry exactly
60 minutes after its creation. The default-location level changes by available `-2`
and reserved `+2`; one `RESERVE` history row exists with that inventory item,
default location, and quantity, using the existing `stock_moves` columns.

Repeat the identical request. Expected: HTTP 200 with original IDs and no extra
reservation, movement, or quantity transfer. Change items/quantities for the same
`orderId`: HTTP 409 `ERR_RESERVATION_CONFLICT`, no writes.

## Atomic failure and concurrency

Submit two variants where one has insufficient stock. Expected: HTTP 422
`ERR_INSUFFICIENT_STOCK` naming the failing variant; no level, request claim,
reservation, or movement is retained for either item.

Verify malformed UUIDs, duplicate IDs, zero/negative/fractional quantities, untracked/
deleted variants, absent default location, and absent levels produce documented errors
without writes.

Run isolated PostgreSQL integration tests with at least 100 competing requests for a
finite-stock variant. Successful held quantity must not exceed initial availability and
both stored quantities stay non-negative:

```bash
go test ./internal/domain/... ./internal/app/inventory/... ./internal/delivery/http/... \
  ./internal/infrastructure/repository/... -count=1
```
The repository integration tests (`inventoryRepo_test.go`) run against a disposable
PostgreSQL database set via `TEST_DATABASE_URL`; they skip automatically when it is unset,
so `go test ./...` stays green without a database.

```bash
export TEST_DATABASE_URL="$DATABASE_URL"
psql "$TEST_DATABASE_URL" -f migrations/0005_checkout_reservation_integrity.sql
go test ./internal/infrastructure/repository/... -count=1
```
Inject failing Redis transport/configuration: expect `ERR_COORDINATION_UNAVAILABLE`
and no PostgreSQL write. Force fake-locker contention: expect a retryable in-progress/
coordination response and no write. Check outcome metrics/logs exclude Redis secrets.

## Documentation checks

```bash
swag init -g cmd/main.go -o docs --parseInternal
go test ./...
go vet ./...
```

Confirm Swagger and the route agree with
[create-reservation.openapi.yaml](contracts/create-reservation.openapi.yaml).
