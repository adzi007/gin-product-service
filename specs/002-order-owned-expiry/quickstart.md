# Quickstart: Validate Order-Owned Reservation Expiry

## Prerequisites

- Go 1.25 with dependencies available through `go mod download`.
- A disposable PostgreSQL database with the base [PRD schema](../../PRD.md), one default
  location, a tracked active variant, inventory item, and inventory level.
- A reachable Redis REST service configured with `REDIS_REST_URL` and
  `REDIS_REST_TOKEN`. Do not print either value.
- `DATABASE_URL`, `APP_ENV`, and `API_JWT_SECRET` configured for the service.

For a fresh disposable database, apply the existing incremental migrations and the new
post-rollout migration, then run the automated checks:

```bash
psql "$DATABASE_URL" -f migrations/0001_add_variant_position.sql
psql "$DATABASE_URL" -f migrations/0002_add_inventory_levels_unique.sql
psql "$DATABASE_URL" -f migrations/0005_checkout_reservation_integrity.sql
psql "$DATABASE_URL" -f migrations/0006_order_owned_reservation_expiry.sql
go test ./...
```

For an existing production-like deployment, use the safe order instead: deploy the new
application revision and drain every old instance first; only then apply migration 0006.
The old revision reads `checkout_reservation_requests`, so do not drop that table while
it can still serve traffic.

## Create and retry an order reservation

Run the service:

```bash
go run cmd/main.go
```

Submit prepared UUIDs. `expiresAt` is an RFC 3339 timestamp with an explicit offset and
at most six fractional-second digits; item IDs are public product variant IDs.

```bash
curl --fail-with-body -X POST http://localhost:5000/api/v1/inventory/reservations \
  -H 'Content-Type: application/json' \
  -d '{
    "orderId":"11111111-1111-4111-8111-111111111111",
    "expiresAt":"2030-01-02T03:04:05.123456+07:00",
    "items":[{"id":"22222222-2222-4222-8222-222222222222","qty":2}]
  }'
```

Expected: HTTP 201, `status: success`, an `ACTIVE` reservation, and a returned
`expiresAt` representing exactly `2030-01-01T20:04:05.123456Z`. The default-location
level changes by available `-2` and reserved `+2`; one `RESERVE` movement exists with
that inventory item, default location, and quantity, using the existing columns.
The service must not substitute a duration.

Repeat the same request, including an equivalent-offset expiry such as
`2030-01-01T20:04:05.123456Z`. Expected: HTTP 200 with original reservation IDs, the
same represented expiry instant, and no extra reservation, movement, or quantity transfer.

Change items, quantity, or expiry for the same `orderId`. Expected: HTTP 409
`ERR_RESERVATION_CONFLICT` and no writes.

## Validate expiry failures and atomicity

- Omit `expiresAt`, send a malformed/offset-less value, or send more than six fractional
  digits. Expected: HTTP 400 `ERR_INVALID_EXPIRY` and no writes.
- Send a well-formed current or past value. Expected: HTTP 422 `ERR_EXPIRED_EXPIRY` and
  no writes.
- Hold a request at the Redis/advisory-lock boundary until its expiry passes. Expected:
  `ERR_EXPIRED_EXPIRY`; no inventory level, reservation, or stock movement from that
  request remains.
- Request multiple variants where one is insufficient. Expected: HTTP 422
  `ERR_INSUFFICIENT_STOCK`, naming only the public `variantId`; no partial hold remains.

Run the focused checks, including PostgreSQL integration coverage against the disposable
database:

```bash
export TEST_DATABASE_URL="$DATABASE_URL"
go test ./internal/domain/... ./internal/app/inventory/... ./internal/delivery/http/... \
  ./internal/infrastructure/repository/... -count=1
go test ./...
go vet ./...
```

The integration suite must exercise at least 100 identical/conflicting retries and two
concurrent, disjoint first requests for one `orderId`. It must prove that one complete
persisted set wins; it must never create a merged order set or over-reserve stock.

## Confirm persistence, telemetry, and documentation

- Inspect the successful rows: every new row for an order has the same `order_id` and
  caller-owned `expires_at`; historic rows retain their prior expiry values.
- Confirm `checkout_reservation_requests` is absent only after the safe post-rollout
  migration, and confirm no new code path reads or writes it.
- Confirm the structured outcome and Prometheus labels distinguish success/retry/conflict
  from `expiry_invalid` and `expiry_expired`, without Redis credentials or request bodies.
- Regenerate and check Swagger against the standalone contract:

```bash
swag init -g cmd/main.go -o docs --parseInternal
git diff --check
```

Compare the generated docs with
[create-reservation.openapi.yaml](contracts/create-reservation.openapi.yaml). The route
remains `/api/v1/inventory/reservations` and the request now requires `expiresAt`.

## Verification status (2026-09-11)

- Automated checks: `go test ./...`, `go vet ./...`, and `git diff --check` pass
  locally. PostgreSQL integration tests are written but skip without
  `TEST_DATABASE_URL`.
- Manual disposable-database/live-Redis quickstart: **not executed** in this
  environment (no disposable PostgreSQL database or reachable Redis REST service
  was available). The required 100-retry and concurrent first-request scenarios
  therefore remain pending live-environment verification.
