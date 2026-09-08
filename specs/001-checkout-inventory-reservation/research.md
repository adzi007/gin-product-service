# Phase 0 Research: Checkout Inventory Reservation

## Decision: follow the service's `/api/v1` base path

- **Decision**: Expose `POST /api/v1/inventory/reservations`; document
  `/inventory/reservations` under the Swagger `/api/v1` base path.
- **Rationale**: The active router groups APIs under `/api/v1`, the README declares
  that base path, and `cmd/main.go` uses `@BasePath /api/v1`. The feature and older
  PRD use `/v1` as a shortened module path.
- **Alternatives considered**: A bare `/v1` duplicate would split public API convention
  and duplicate a handler without a versioning requirement.

## Decision: build a dedicated inventory module

- **Decision**: Add reservation entities/errors and narrow use-case, repository, and
  locker ports to `internal/domain/inventory.go`; implement the application behavior in
  `internal/app/inventory`, a PostgreSQL repository, a Redis REST locker, Gin DTO/
  handler, router registration, and explicit wire composition.
- **Rationale**: `internal/app/inventory` contains only `.gitkeep`; the domain already
  owns `Quantity`, inventory levels, and `RESERVE`/`UNRESERVE` move types. This
  preserves the repository -> use case -> handler flow instead of extending product code.
- **Alternatives considered**: `productRepo.AdjustVariantStock` changes an absolute
  stock target and lacks reservation/idempotency locks. Handler workflow logic violates
  clean architecture.

## Decision: Redis REST lease plus PostgreSQL transaction

- **Decision**: Implement a fail-closed Redis REST `ReservationLocker` with Go
  `net/http`. Use Upstash `EVAL` to atomically acquire all canonical order/variant
  keys with a random owner token and PX TTL; use a token-checked Lua release. PostgreSQL
  remains the durable and final correctness authority.
- **Rationale**: `REDIS_REST_URL` and `REDIS_REST_TOKEN` are REST credentials, not a
  TCP Redis endpoint. One script avoids partial sequential lock acquisition. PostgreSQL
  `FOR UPDATE` blocks concurrent writers/lockers on the same rows and remains safe if a
  Redis lease expires.
- **Alternatives considered**: Redis-only state cannot commit atomically with PostgreSQL
  history. A TCP client does not match configuration. Redis without database locks fails
  on TTL expiry. PostgreSQL-only locking is stock-safe but misses FR-009's shared
  coordination dependency. Sequential `SET NX` can partially acquire locks.

## Decision: durable order-level idempotency fingerprint

- **Decision**: Add `checkout_reservation_requests` keyed by `order_id` with a
  SHA-256 fingerprint of sorted `(variant_id, quantity)` request items. Add
  `UNIQUE(order_id, inventory_item_id)` on reservations as a duplicate-item backstop.
- **Rationale**: One order produces multiple reservation rows, so it needs a durable
  parent claim to record which complete request first succeeded. Matching fingerprints
  return persisted results; another fingerprint returns conflict.
- **Alternatives considered**: Child-row comparison alone cannot safely claim an order
  before those rows exist. Redis idempotency vanishes after TTL expiry or data loss.

## Decision: deterministic PostgreSQL locks and all-or-nothing writes

- **Decision**: In one pgx transaction resolve the default location and eligible
  variants, lock `inventory_levels` with `SELECT ... FOR UPDATE` ordered by inventory
  item ID, validate all stock, update each level, and insert every reservation and linked
  `RESERVE` movement before one commit.
- **Rationale**: Rollback leaves no partial hold. Deterministic order reduces multi-item
  deadlocks, and availability is checked after locks, preventing over-reservation.
- **Alternatives considered**: Read then unconditional update loses concurrent changes.
  A transaction per item permits partial checkout holds.

## Decision: strengthen persistence invariants in a forward-only migration

- **Decision**: Create the request table; require new reservation rows to have an order,
  positive quantity, and `expires_at > reserved_at`; add order/item uniqueness, an
  optional unique `stock_moves.reservation_id` FK, non-null/non-negative inventory
  quantities, and a partial unique default-location index. Validate/backfill existing
  data before enabling strict constraints.
- **Rationale**: The PRD base schema lacks these integrity/idempotency constraints. A
  direct reservation-to-movement link is needed for future reconciliation.
- **Alternatives considered**: Application-only checks do not protect future writers;
  parsing free-text movement reasons is not reconciliation-safe.

## Decision: stable errors and no-secret telemetry

- **Decision**: Return `ERR_VALIDATION` (400), `ERR_VARIANT_NOT_FOUND` or
  `ERR_INVENTORY_NOT_FOUND` (404), `ERR_VARIANT_NOT_RESERVABLE` or reservation
  conflicts (409), `ERR_INSUFFICIENT_STOCK` (422), and
  `ERR_COORDINATION_UNAVAILABLE` (503). Log/measure outcomes without credentials or
  customer data.
- **Rationale**: This distinguishes required failure classes and follows the existing
  `status`/`code` response convention.
- **Alternatives considered**: Raw infrastructure errors leak internals; one generic 500
  fails the stable-error requirement.

## Sources consulted

- [README.md](../../README.md): active architecture, router base path, dependencies,
  tests, and Redis configuration names.
- [Feature specification](spec.md): required behavior, endpoint, idempotency, lifetime,
  and success criteria.
- [PostgreSQL explicit locking](https://www.postgresql.org/docs/17/explicit-locking.html):
  `FOR UPDATE` serialization semantics.
- [Upstash REST API](https://upstash.com/docs/redis/features/restapi),
  [Lua scripting](https://upstash.com/blog/lua-scripting-on-upstash-redis-atomic-operations-over-http),
  and [key-based locking](https://upstash.com/docs/redis/features/key-locking.html):
  REST authentication and atomic explicit-key scripts.
