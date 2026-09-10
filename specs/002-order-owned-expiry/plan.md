# Implementation Plan: Order-Owned Reservation Expiry

**Branch**: `002-order-owned-expiry` | **Date**: 2026-09-10 | **Spec**: [spec.md](spec.md)

## Summary

Change `POST /api/v1/inventory/reservations` so the order service supplies a
required `expiresAt` instant. New reservations persist that caller-owned instant
unchanged instead of calculating a 60-minute lifetime. Remove the durable
`checkout_reservation_requests` parent claim: the complete set of reservation rows
for an `order_id` becomes the sole durable idempotency record. A transaction-scoped
PostgreSQL advisory lock serializes first requests for one order; the existing
PostgreSQL row locks and Redis REST lease continue to protect stock under concurrent
requests.

## Technical Context

**Language/Version**: Go 1.25.0

**Primary Dependencies**: Gin 1.12.0; pgx/v5 5.10.0 with pgxpool; google/uuid;
go-playground/validator; zap; Prometheus; swaggo. The existing standard-library
Redis REST adapter remains unchanged.

**Storage**: PostgreSQL is the authoritative durable store. `reservations` stores
the order association and supplied `timestamptz` expiry. Upstash Redis REST provides
only a fixed five-second, fail-closed coordination lease.

**Testing**: Standard-library Go unit tests for domain/use-case/DTO behavior and
handler contract tests; PostgreSQL integration tests with `TEST_DATABASE_URL` for
migration, transaction, locking, retry, and rollback behavior. No separate
performance test is planned; the specification's 100-request check is a functional
idempotency scenario.

**Target Platform**: Linux-hosted Go HTTP service on port 5000.

**Project Type**: HTTP microservice.

**Performance Goals**: Preserve the existing bounded request path and fixed 5-second
Redis lease. Functional validation includes at least 100 repeated identical/conflicting
requests; it is not a throughput benchmark.

**Constraints**: One request is all-or-nothing; quantities remain positive whole
numbers; new entity IDs are UUIDv7; all new holds for an order share one caller-supplied
future expiry; the inventory service applies no duration policy; Redis failure or
contention changes no inventory; PostgreSQL remains correct after a Redis lease expires;
credentials and customer-sensitive data must not be logged or returned.

**Scale/Scope**: One existing endpoint and inventory module. The change affects its
DTO/domain/repository flow, PostgreSQL migration, generated Swagger, README, tests,
and operational telemetry. It supports multi-item orders and concurrent callers across
service instances.

## Constitution Check

### Pre-design gate

| Gate | Status | Plan response |
|---|---|---|
| I. Domain-Centered Clean Architecture | PASS | HTTP parses/maps the request; `app/inventory` orchestrates domain ports; PostgreSQL and Redis stay in infrastructure; `internal/wire` needs no new dependency shape. |
| II. Domain-Driven Business Integrity | PASS | A single transaction serializes each order, compares persisted holds, locks levels deterministically, and writes all reservation/movement/quantity changes atomically. |
| III. SOLID Contracts and Explicit Composition | PASS | Extend the existing narrow reservation input/use-case/repository contracts with expiry rather than adding a parent-request abstraction. |
| IV. PostgreSQL Correctness and pgx/v5 | PASS | Use parameterized pgx queries, a transaction-scoped advisory lock, `FOR UPDATE` row locks, explicit transaction boundaries, retained database constraints, and a documented forward-only migration. |
| V. Verification and Operational Visibility | PASS | Update unit, handler, and PostgreSQL integration coverage; retain structured outcomes and metrics, add expiry outcomes, and regenerate public API documentation. |

No constitutional exception is required.

## Project Structure

### Documentation

```text
specs/002-order-owned-expiry/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── contracts/
    └── create-reservation.openapi.yaml
```

### Source Code

```text
docs/
├── docs.go                              # regenerated Swagger document
├── swagger.json                         # regenerated Swagger document
└── swagger.yaml                         # regenerated Swagger document

internal/
├── app/inventory/
│   ├── reservationUseCase.go             # expiry validation, canonicalization, lease orchestration
│   └── reservationUseCase_test.go
├── delivery/http/
│   ├── dto/inventory_dto.go              # expiresAt parsing and UTC response formatting
│   └── handler/
│       ├── inventory_handler.go          # stable expiry error mapping and Swagger annotations
│       └── inventory_handler_test.go
├── domain/
│   ├── inventory.go                      # expiry input and stable error contracts
│   └── inventory_test.go
└── infrastructure/repository/
    ├── inventoryRepo.go                  # order lock, reservation-set retry comparison, atomic persistence
    ├── inventoryRepo_test.go              # PostgreSQL transaction/migration coverage
    └── model/reservation_model.go         # remove parent-request row model

migrations/
└── 0006_order_owned_reservation_expiry.sql # post-rollout parent-request table removal

README.md                                  # caller-owned expiry and deployment/migration guidance
```

**Structure Decision**: Modify the established inventory module and its existing
clean-architecture flow. No new module, storage system, or external dependency is
needed.

## Design Decisions

### Caller-owned expiry contract

1. Require `expiresAt` with `orderId` and `items`. It is an RFC 3339 timestamp with
   an explicit UTC offset and at most six fractional-second digits, matching PostgreSQL
   `timestamptz` precision. Normalize it to UTC before it crosses the delivery boundary.
2. Treat absent, malformed, offset-less, or over-precision values as
   `ERR_INVALID_EXPIRY` (HTTP 400), distinct from general item validation. A parsed
   value that is no longer strictly future at creation is `ERR_EXPIRED_EXPIRY`
   (HTTP 422), distinct from insufficient stock and conflict.
3. Thread the normalized expiry through `CreateReservationUseCase` and
   `CreateReservationInput`. Remove the SHA-256 fingerprint field and helper because
   request identity is derived from durable reservation rows, not a separate request row.
4. Serialize response values with `time.RFC3339Nano`; acceptance checks compare parsed
   instants, not input spelling. Thus equivalent offsets represent the same retry while
   the stored instant is returned without seconds-only truncation.

### Order-owned idempotency and concurrency

1. Retain the existing Redis REST Lua lease over the canonical order/variant keys with
   `PX 5000`. It remains fail-closed coordination only; no transaction starts if it
   cannot be acquired.
2. After beginning the pgx transaction, acquire a deterministic transaction-scoped
   PostgreSQL advisory lock derived from the complete `order_id`. Collisions can only
   serialize unrelated orders; they cannot return a wrong result. This lock covers the
   no-row case that `FOR UPDATE` and `UNIQUE(order_id, inventory_item_id)` cannot lock.
3. Load that order's reservations with `FOR UPDATE OF r`, joining `inventory_items` to
   obtain public variant IDs. If rows exist, compare canonical sorted `(variant_id,
   quantity)` pairs and the single represented expiry instant. An exact match returns
   the persisted result unchanged; any difference is `ERR_RESERVATION_CONFLICT` with
   no inventory write. Do not create a second set for an existing order, regardless of
   current lifecycle status.
4. If no rows exist, obtain PostgreSQL `clock_timestamp()` after the order lock and use
   that one timestamp as `reserved_at` for the entire new set. Reject an expiry not
   strictly after it before a quantity update. This catches requests that become stale
   while waiting for Redis or database locks.
5. Preserve the existing default-location lookup, variant-to-inventory resolution,
   deterministic `inventory_levels FOR UPDATE` locking, availability recheck, and one
   transaction for level transfers, `ACTIVE` reservations, and `RESERVE` stock
   movements. Insert the caller's normalized expiry as a query parameter for every
   reservation; never calculate `now() + interval '60 minutes'`.

### Persistence migration and rollout

- Keep `0005_checkout_reservation_integrity.sql` unchanged. It already makes new
  reservation `order_id` values non-null and supplies useful positive-quantity,
  expiry-after-reserved, and `(order_id, inventory_item_id)` safeguards.
- Add forward-only `0006_order_owned_reservation_expiry.sql` to drop only
  `checkout_reservation_requests`. Do not modify, backfill, recalculate, or delete any
  reservation, stock movement, inventory level, or historical expiry value.
- The new application revision must stop reading and writing the parent table and is
  compatible while that table still exists. Because migrations are manual, deploy and
  drain all old application instances first, then apply `0006`; older instances query
  the table and would otherwise fail. The removed fingerprints and claim timestamps are
  intentionally redundant data, not an audit record required by this feature.

### Public API and observability

The route remains `POST /api/v1/inventory/reservations`. Update the standalone OpenAPI
contract, handler annotations, and generated Swagger files with required `expiresAt`,
canonical timestamp behavior, and the two new stable expiry errors. Update README
examples and migration order. Keep reservation attempt/latency metrics and structured
logs; emit `expiry_invalid` and `expiry_expired` outcomes without logging request bodies,
Redis credentials, or endpoint URLs.

### Verification strategy

- Domain/use-case tests: missing/malformed expiry, deterministic UTC canonicalization,
  equivalent-offset retries, expiry-inclusive input propagation, expired handling, and
  Redis fail-closed behavior.
- Handler/DTO tests: required `expiresAt`, distinct stable status/code mapping, exact
  response instant formatting, unchanged 201/200 semantics, and no internal identifier
  leakage.
- PostgreSQL integration tests: migration 0006 removes only the parent table; new
  reservations retain order and caller expiry; identical retry produces no additional
  writes; changed item, quantity, or expiry conflicts; expired-during-processing rolls
  back; and concurrent disjoint first requests for one order yield exactly one complete
  order set. Preserve existing stock and historic-reservation regression coverage.
- Documentation/quality: regenerate Swagger, validate the standalone OpenAPI YAML,
  run `go test ./...`, `go vet ./...`, and `git diff --check`. Use a disposable database
  and live Redis REST configuration for the manual end-to-end quickstart; state any
  unavailable integration environment explicitly.

## Post-design Constitution Check

All gates remain **PASS**. The design eliminates a redundant persistence abstraction
without moving business rules into HTTP or infrastructure, and replaces its correctness
role with a PostgreSQL transaction lock plus row-owned comparison. It retains atomic
inventory integrity, parameterized pgx/v5 access, migration safety, error observability,
and verification at the domain, HTTP, and PostgreSQL layers.

## Complexity Tracking

No constitutional violations or justified exceptions.
