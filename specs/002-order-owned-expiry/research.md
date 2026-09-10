# Phase 0 Research: Order-Owned Reservation Expiry

## Decision: require a caller-owned RFC 3339 expiry instant

- **Decision**: Add required `expiresAt` to the existing create-reservation request.
  Accept RFC 3339 timestamps with an explicit UTC offset and no more than six
  fractional-second digits, normalize them to UTC, and return them in RFC3339Nano
  format.
- **Rationale**: PostgreSQL `timestamptz` stores microsecond precision. Limiting input
  to that precision preserves the represented instant through persistence and response;
  UTC normalization makes different valid offset spellings compare identically.
- **Alternatives considered**: Retaining the 60-minute calculation violates order
  ownership. Accepting nanosecond precision would silently round or alter an instant in
  PostgreSQL. Comparing input strings would incorrectly treat equivalent offsets as
  different requests.

## Decision: distinguish invalid from no-longer-future expiry values

- **Decision**: Return `ERR_INVALID_EXPIRY` with HTTP 400 for absent/malformed/
  unsupported-precision input and `ERR_EXPIRED_EXPIRY` with HTTP 422 for a parsed instant
  that is not strictly after the database creation timestamp.
- **Rationale**: The specification requires these failures to be distinct from one
  another, stock availability, general validation, and idempotency conflict. A
  well-formed but already elapsed time is semantically unprocessable, while malformed
  input is a bad request.
- **Alternatives considered**: A single `ERR_VALIDATION` code makes consumers branch on
  messages. A conflict response for an elapsed new request misstates what failed.

## Decision: use reservation rows as the durable idempotency record

- **Decision**: Remove the `checkout_reservation_requests` row model, its SHA-256
  fingerprint, and all reads/writes of that table. Within a transaction, load the order's
  persisted reservation set and compare its canonical `(variant_id, quantity)` set plus
  expiry instant with the request.
- **Rationale**: Each reservation already stores `order_id`, quantity, expiry, and its
  inventory-item association. These rows fully identify the original request and are the
  durable business record needed by later lifecycle behavior.
- **Alternatives considered**: Retaining the parent request table contradicts the feature
  boundary. Redis-only idempotency disappears after its short lease. Storing another
  request fingerprint on reservation rows duplicates facts that can be compared directly.

## Decision: serialize each order with a PostgreSQL transaction advisory lock

- **Decision**: Acquire a deterministic `pg_advisory_xact_lock` derived from `order_id`
  before querying that order's reservations. Then use `FOR UPDATE OF r` for existing
  rows and the existing deterministic inventory-level locks for new work.
- **Rationale**: A unique `(order_id, inventory_item_id)` constraint cannot prevent two
  simultaneous first requests with different item sets, because neither sees an existing
  row. Redis leases can expire. A transaction advisory lock closes the zero-row race and
  releases automatically at commit or rollback; PostgreSQL remains authoritative.
- **Alternatives considered**: Locking only existing reservation rows has no effect for a
  new order. A table-level lock is needlessly broad. Restoring a separate parent claim
  violates the requested record ownership.

## Decision: validate expiry at the database creation point

- **Decision**: For a new order set, read `clock_timestamp()` after the order lock and
  use that exact database value as every new row's `reserved_at`. Reject `expiresAt <=`
  that value before any level transfer, and parameterize the supplied expiry into inserts.
- **Rationale**: A request can become expired while it waits for coordination or database
  locks. A clock sampled at HTTP receipt or a transaction-start `now()` does not model
  the actual reservation creation point as accurately.
- **Alternatives considered**: Validating only in the handler permits stale requests to
  reach the transaction. Using `now() + interval '60 minutes'` retains the prohibited
  inventory-owned policy.

## Decision: preserve historical reservation data and stage table removal safely

- **Decision**: Add forward-only migration `0006_order_owned_reservation_expiry.sql`
  that drops only `checkout_reservation_requests`, after all old application instances
  are drained. Leave migration 0005 and all historical reservation rows untouched.
- **Rationale**: The current parent table has no foreign-key dependency, while old code
  still queries it. New code works while it remains, allowing an application-first
  rollout; dropping it later removes only redundant fingerprint/claim metadata.
- **Alternatives considered**: Editing 0005 rewrites migration history. Recalculating
  historical expiry violates the preservation requirement. Dropping the table before
  replacing every old instance breaks a rolling deployment.

## Decision: retain existing coordination and observability boundaries

- **Decision**: Keep the existing Redis REST Lua lease at `PX 5000`, default-location
  behavior, structured logs, and reservation metrics. Add expiry-invalid and
  expiry-expired outcomes; regenerate Swagger and update README.
- **Rationale**: The feature changes expiry ownership and durable idempotency, not the
  multi-instance coordination or public route. Existing metrics and no-secret logging
  satisfy the operational baseline when their new failure outcomes are observable.
- **Alternatives considered**: Removing Redis weakens currently required fail-closed
  coordination. Treating it as durable correctness would be unsafe on TTL expiry.

## Sources consulted

- [Feature specification](spec.md): caller-owned expiry, idempotency, historical-data,
  and stable-error requirements.
- [Current reservation plan](../001-checkout-inventory-reservation/plan.md) and
  [research](../001-checkout-inventory-reservation/research.md): established route,
  Redis lease, PostgreSQL locking, and observability decisions.
- `internal/domain/inventory.go`, `internal/app/inventory/reservationUseCase.go`, and
  `internal/infrastructure/repository/inventoryRepo.go`: current contracts, 60-minute
  assignment, fingerprint claim, and transaction behavior.
- `migrations/0005_checkout_reservation_integrity.sql`: deployed reservation constraints
  and the removable parent-request table.
- `internal/delivery/http/dto/inventory_dto.go`, handler, router, Swagger files, README,
  and existing unit/integration tests: public-contract and validation surface.
