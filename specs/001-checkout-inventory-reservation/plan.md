# Implementation Plan: Checkout Inventory Reservation

**Branch**: `development` | **Date**: 2026-09-09 | **Spec**: [spec.md](spec.md)

## Summary

Add `POST /api/v1/inventory/reservations` for an order service to atomically hold
unique product-variant quantities at the default fulfillment location. The inventory
module will validate and canonicalize the request, acquire a short-lived Redis REST
lease for the order and variants, then use one PostgreSQL transaction with deterministic
row locks to persist idempotency state, reservations, `RESERVE` stock movements, and
the available-to-reserved quantity transfer. PostgreSQL remains the durable correctness
authority; Redis is fail-closed coordination required across service instances.

## Technical Context

**Language/Version**: Go 1.25.0

**Primary Dependencies**: Gin 1.12.0; pgx/v5 5.10.0 with pgxpool; google/uuid (UUIDv7);
go-playground/validator; zap; Prometheus. Use Go standard-library `net/http` for the
Upstash Redis REST adapter, avoiding an incompatible Redis TCP client.

**Storage**: PostgreSQL is authoritative durable storage. Upstash Redis REST is a
short-lived, non-durable lock coordinator only.

**Testing**: Standard-library `testing`, hand-written domain-port fakes, Gin handler/
route tests, and isolated PostgreSQL integration tests for migrations, transactions, and
concurrent reservation behavior.

**Target Platform**: Linux-hosted Go HTTP service on port 5000.

**Project Type**: HTTP microservice.

**Performance Goals**: 95% of normal requests receive a definitive result within one
second; validation, Redis, and database time budgets remain bounded below the lease TTL.

**Constraints**: A request is all-or-nothing; quantities are non-negative integers; all
new IDs are UUIDv7; reservations expire 60 minutes after the database-assigned creation
timestamp; Redis failure or lease contention makes no inventory change; same `orderId`
must be durably idempotent; credentials never appear in logs or responses.

**Scale/Scope**: One new create endpoint and inventory module; multi-item orders;
concurrent callers across instances; schema migrations, Swagger output, metrics, logs,
and focused unit/handler/PostgreSQL integration coverage.

## Constitution Check

### Pre-design gate

| Gate | Status | Plan response |
|---|---|---|
| I. One-way clean architecture | PASS | Handler maps HTTP only; `app/inventory` orchestrates domain ports; PostgreSQL and Redis REST implementations stay in infrastructure; wiring is explicit. |
| II. Business integrity and atomic writes | PASS | One pgx transaction locks inventory levels, checks stock, updates quantities, and writes every reservation/history row; deterministic ordering prevents lock-order deadlocks. |
| III. Narrow contracts and explicit composition | PASS | Introduce separate `ReservationRepository` and `ReservationLocker` domain ports; construct the Redis adapter in the composition root with explicit configuration. |
| IV. PostgreSQL correctness | PASS | Parameterized pgx queries, versioned forward-only migration, database constraints, `FOR UPDATE` locking, and exact integer `domain.Quantity` values. |
| V. Verification and visibility | PASS | Add use-case, handler/route, real-PostgreSQL integration, concurrent, migration, metric, structured-log, and Swagger checks. |

No constitutional exception is required. The Redis REST adapter is a real external
coordination boundary, not a replacement persistence store; PostgreSQL remains the
source of truth.

## Project Structure

### Documentation

```text
specs/001-checkout-inventory-reservation/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── contracts/
    └── create-reservation.openapi.yaml
```

### Source Code

```text
cmd/server/
└── gin_server.go                         # construct/inject Redis locker

internal/
├── domain/
│   └── inventory.go                       # Reservation types, errors, use-case/repository/locker ports
├── app/inventory/
│   └── reservationUseCase.go              # validation, canonicalization, orchestration, logs
├── delivery/http/
│   ├── dto/inventory_dto.go               # request DTO
│   ├── handler/inventory_handler.go       # response/error mapping and Swagger annotations
│   └── router.go                          # /api/v1/inventory/reservations route
├── infrastructure/
│   ├── redis/reservation_locker.go        # Redis REST Lua lease adapter
│   └── repository/
│       ├── inventoryRepo.go               # pgx transaction and reservation persistence
│       └── model/reservation_model.go      # DB row mapper
└── wire/container.go                       # repository -> use case -> handler composition

migrations/
└── 0005_checkout_reservation_integrity.sql # idempotency, audit, and quantity constraints
```

**Structure Decision**: Add the feature as a dedicated inventory module. It uses the
repository-to-use-case-to-handler flow already used by category/product modules and
does not add a cross-layer dependency to the existing product repository.

## Design Decisions

### Request flow and consistency boundary

1. Bind `{orderId, items:[{id, qty}]}` at the handler. Reject missing/malformed UUIDs,
   an empty collection, non-positive/non-integer quantities, and duplicate variant IDs.
2. Canonicalize items by variant UUID and calculate a SHA-256 request fingerprint.
3. Acquire a Redis REST lease for the canonical key set: the order key followed by all
   variant keys in lexical order. A single Upstash Lua `EVAL` verifies that every key is
   absent and assigns every key the same random owner token with a short PX TTL.
4. Begin one PostgreSQL transaction only after the lease succeeds. Claim or lock an
   order-level idempotency row. An equal fingerprint returns its persisted reservations
   unchanged; a different fingerprint returns conflict.
5. Resolve one default location, then resolve each non-deleted, tracked variant to its
   inventory item and level. Lock all level rows with `FOR UPDATE` in deterministic
   inventory-item order.
6. Check every quantity while those rows are locked. On any missing/non-reservable
   inventory or insufficient quantity, roll back the whole transaction.
7. For every item, move `available_qty` to `reserved_qty`, insert one `ACTIVE`
   reservation with `expires_at = reserved_at + 60 minutes`, and insert its linked
   `RESERVE` stock movement. Commit once.
8. Release only lease keys owned by this request via a second Lua script. If release
   fails after commit, log/measure it and allow TTL expiry; never undo committed data.

The database row locks and re-checks are mandatory even with Redis: a lease can expire
while a process is paused, and every inventory writer must remain safe against another
writer. No transaction is opened on Redis unavailability, timeout, or lock contention.

### Configuration and observability

`cmd/server` reads `REDIS_REST_URL` and `REDIS_REST_TOKEN` and explicitly injects a
locker into `wire.NewContainer`. Missing or unusable coordination configuration is
represented by a fail-closed locker so the reservation endpoint returns a retryable
coordination error without changing inventory; unrelated endpoints can still start.
The adapter sends the token only as an HTTP Authorization header and never logs it.

Emit structured events for `succeeded`, `retried`, `rejected`, `conflict`, and
`coordination_failed`, with `order_id`, outcome, and affected variant ID where
relevant. Add reservation attempt/outcome counters and latency; preserve the existing
database operation histogram as `inventory/create_reservation`.

### API base-path resolution

The feature/legacy PRD abbreviates the endpoint as `/v1/inventory/reservations`. This
service's router and Swagger base path are `/api/v1` (README and `cmd/main.go`), so the
implemented public route is **`POST /api/v1/inventory/reservations`** and the OpenAPI
path is `/inventory/reservations` beneath that base path.

## Post-design Constitution Check

All gates remain **PASS**. The design keeps business rules in the inventory domain/use
case, leaves HTTP and Redis/PostgreSQL details outside the domain, and supplies a
durable atomic transaction plus database constraints rather than relying on distributed
locks alone. The migration, integration tests, docs, logs, and metrics close the
Constitution V requirements.

## Complexity Tracking

No constitutional violations or justified exceptions.
