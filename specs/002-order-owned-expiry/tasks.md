---

description: "Dependency-ordered implementation tasks for order-owned reservation expiry"
---

# Tasks: Order-Owned Reservation Expiry

**Input**: Design documents from `/specs/002-order-owned-expiry/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [quickstart.md](quickstart.md), and [contracts/create-reservation.openapi.yaml](contracts/create-reservation.openapi.yaml)

**Tests**: Required. The specification, plan, quickstart, and constitution require focused domain, use-case, HTTP, and PostgreSQL integration coverage. The required 100-retry scenario is functional idempotency coverage, not a performance benchmark.

**Organization**: Tasks are grouped by user story so each increment has a clear goal and independent verification path. User Story 2 extends the caller-expiry contract from User Story 1; User Story 3's post-rollout cleanup depends on the row-owned retry implementation from User Story 2.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Prepare the forward-only database change without rewriting deployed migration history.

- [X] T001 Create the post-rollout migration that drops only `checkout_reservation_requests`, preserves all reservation/history rows, and documents the old-instance drain prerequisite in `migrations/0006_order_owned_reservation_expiry.sql`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Establish the shared domain contract that every delivery and persistence change consumes.

**⚠️ CRITICAL**: Complete this phase before changing the reservation delivery, application, or repository flow.

- [X] T002 Replace fingerprint-based reservation input with normalized caller-owned `ExpiresAt`, extend the create-use-case signature, and add stable invalid/expired expiry errors in `internal/domain/inventory.go`

**Checkpoint**: The domain expresses the new ownership boundary; user-story work can proceed.

---

## Phase 3: User Story 1 - Honor an Order-Selected Expiry (Priority: P1) 🎯 MVP

**Goal**: Accept one explicit, future order-owned expiry instant and persist/return that exact instant for every new hold without an inventory-service lifetime policy.

**Independent Test**: Submit a valid multi-item request with an explicit-offset future `expiresAt` and verify that every returned and persisted hold has the same instant. Submit missing, malformed, over-precision, current, and past values and verify the documented error and no inventory/reservation/movement writes.

### Tests for User Story 1

- [X] T003 [P] [US1] Add domain contract tests for caller-owned expiry input and distinct invalid/expired errors in `internal/domain/inventory_test.go`
- [X] T004 [P] [US1] Add request/response conversion tests for required explicit-offset RFC3339 timestamps, six-digit precision, UTC normalization, and RFC3339Nano output in `internal/delivery/http/dto/inventory_dto_test.go`
- [X] T005 [P] [US1] Add use-case tests proving expiry is passed unchanged to the repository, no duration is calculated, and Redis lease acquisition remains fail-closed in `internal/app/inventory/reservationUseCase_test.go`
- [X] T006 [P] [US1] Add handler contract tests for `expiresAt`, `ERR_INVALID_EXPIRY` (400), `ERR_EXPIRED_EXPIRY` (422), and exact successful response timestamps in `internal/delivery/http/handler/inventory_handler_test.go`
- [X] T007 [US1] Add PostgreSQL integration cases for exact caller expiry persistence, authoritative creation-time expiry rejection, rollback, and preserved historic expiry rows in `internal/infrastructure/repository/inventoryRepo_test.go`

### Implementation for User Story 1

- [X] T008 [US1] Parse and validate required `expiresAt` at the HTTP boundary, normalize it to UTC, and format response instants with RFC3339Nano in `internal/delivery/http/dto/inventory_dto.go`
- [X] T009 [US1] Pass normalized expiry through the create endpoint, map stable expiry errors to their documented HTTP responses, safely emit the invalid-expiry outcome, and update Swagger annotations in `internal/delivery/http/handler/inventory_handler.go`
- [X] T010 [US1] Canonicalize the caller expiry for repository input, remove application-layer SHA-256 fingerprint generation, retain sorted variant lease keys, and record `expiry_expired` outcomes without logging request bodies or credentials in `internal/app/inventory/reservationUseCase.go`
- [X] T011 [US1] Capture one `clock_timestamp()` after order coordination, reject expiry at or before that time, insert the supplied expiry/reserved time/order ID into every new reservation, and use an expiry-inclusive transitional parent claim until US2 replaces it in `internal/infrastructure/repository/inventoryRepo.go`

**Checkpoint**: A valid request creates ACTIVE holds with its supplied expiry; invalid or expired values cannot change inventory.

---

## Phase 4: User Story 2 - Retry an Order Without Changing Its Hold (Priority: P1)

**Goal**: Make retries compare the persisted reservation set so only an order ID with identical canonical items, quantities, and expiry returns the original holds.

**Independent Test**: Create a reservation, repeat it at least 100 times with equivalent timestamp offsets, and verify no additional rows, movements, or quantity transfers. Change an item, quantity, or expiry for the same order and verify `ERR_RESERVATION_CONFLICT` with no writes.

### Tests for User Story 2

- [X] T012 [US2] Replace fingerprint assertions with canonical item-and-expiry propagation, equivalent-offset retry, conflict, and expiry outcome assertions in `internal/app/inventory/reservationUseCase_test.go`
- [X] T013 [US2] Add PostgreSQL integration coverage for 100 identical retries, changed item/quantity/expiry conflicts, no duplicate movements/transfers, and concurrent disjoint first requests for one order producing exactly one complete set in `internal/infrastructure/repository/inventoryRepo_test.go`

### Implementation for User Story 2

- [X] T014 [US2] Use a deterministic transaction-scoped advisory lock per order, lock/read that order's reservation rows with public variant IDs, return an exact persisted set on retry, and reject every mismatch without writes in `internal/infrastructure/repository/inventoryRepo.go`
- [X] T015 [US2] Delete the transitional parent-claim SQL and expiry-inclusive fingerprint helper so retry/conflict decisions use only persisted reservation rows in `internal/infrastructure/repository/inventoryRepo.go`

**Checkpoint**: Retried orders are idempotent by durable reservation data, not mutable request metadata, even after a Redis lease expires.

---

## Phase 5: User Story 3 - Keep Order Association With Each Hold (Priority: P2)

**Goal**: Retain `order_id` and the caller-owned expiry on each reservation row, then remove the redundant order-level checkout-request record safely after application rollout.

**Independent Test**: Create a multi-item reservation and verify all rows can be found by `order_id`, retain the same expiry, and alone determine retry/conflict. Verify migration 0006 removes only the parent-request table while historical reservation data remains unchanged.

### Tests for User Story 3

- [X] T016 [US3] Add migration and multi-item persistence integration cases proving `order_id`/expiry live on every reservation, reservation rows alone drive retry lookup, and migration 0006 preserves historical records in `internal/infrastructure/repository/inventoryRepo_test.go`

### Implementation for User Story 3

- [X] T017 [US3] Remove the obsolete checkout-reservation-request persistence model and all remaining parent-claim references in `internal/infrastructure/repository/model/reservation_model.go`

**Checkpoint**: The reservation set is the sole durable order association; the parent table is removable only after the new application revision is deployed and old instances are drained.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Complete public documentation, rollout guidance, and required verification.

- [X] T018 [P] Regenerate Swagger documentation from the updated handler contract in `docs/docs.go`
- [X] T019 [P] Regenerate the JSON Swagger artifact for required `expiresAt` and stable expiry errors in `docs/swagger.json`
- [X] T020 [P] Regenerate the YAML Swagger artifact for required `expiresAt` and stable expiry errors in `docs/swagger.yaml`
- [X] T021 Update the caller request example, equivalent-offset retry behavior, error semantics, and application-before-migration rollout order in `README.md`
- [X] T022 Validate the standalone API contract against the generated Swagger request/response behavior in `specs/002-order-owned-expiry/contracts/create-reservation.openapi.yaml`
- [X] T023 Run focused unit, handler, repository integration, full test, vet, and diff checks documented in `specs/002-order-owned-expiry/quickstart.md`
- [X] T024 Execute the disposable-database/live-Redis manual quickstart and record any unavailable environment as incomplete verification in `specs/002-order-owned-expiry/quickstart.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: T001 can start immediately. It creates the forward-only migration but does not authorize applying it before rollout.
- **Foundational (Phase 2)**: T002 depends on the existing reservation vertical slice and blocks the changed delivery/application/repository contracts.
- **User Story 1 (Phase 3)**: Depends on T002. It is the MVP and establishes caller-owned expiry creation and error behavior.
- **User Story 2 (Phase 4)**: Depends on the US1 contract/persistence work. It replaces fingerprint idempotency with persisted-set comparison.
- **User Story 3 (Phase 5)**: Depends on US2 because the parent record cannot be removed until row-owned retry/conflict behavior is implemented.
- **Polish (Phase 6)**: T018-T022 depend on the public implementation; T023 follows all implementation/test additions; T024 follows an available disposable database and live Redis REST environment.

### User Story Dependencies

- **US1 (P1)**: Starts after T002 and is independently deliverable as caller-selected-expiry behavior while the parent claim remains during a rolling rollout.
- **US2 (P1)**: Starts after US1. It consumes the canonical expiry input and makes retries durable from reservation rows.
- **US3 (P2)**: Starts after US2. It removes redundant persistence only after the replacement logic exists.

### Within Each User Story

- Write the listed tests before their corresponding implementation tasks.
- Complete delivery parsing and domain contract propagation before invoking persistence.
- In the repository, obtain the order lock before inspecting rows or validating database creation time; retain deterministic inventory-level locks and the one transaction boundary.
- Do not add an expiry-release worker, a lifetime policy, or a performance benchmark in this feature.

## Parallel Opportunities

- T003-T006 target separate test files and can run in parallel after T002.
- T018-T020 are separate generated Swagger files and can run in parallel once the handler annotations are final.
- During US1 implementation, separate contributors may prepare the handler/DTO work and the repository integration cases, but T011 must consume the contract finalized by T008-T010.

## Parallel Example: User Story 1

```text
Task: "Add domain contract tests in internal/domain/inventory_test.go"
Task: "Add DTO conversion tests in internal/delivery/http/dto/inventory_dto_test.go"
Task: "Add use-case tests in internal/app/inventory/reservationUseCase_test.go"
Task: "Add handler contract tests in internal/delivery/http/handler/inventory_handler_test.go"
```

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Create T001 and complete T002.
2. Complete T003-T011 to accept, validate, persist, and return caller-owned expiry.
3. Run the US1 portion of T023 against a disposable PostgreSQL database.
4. Demonstrate valid, invalid, and expired requests without applying migration 0006 to a shared deployment.

### Incremental Delivery

1. Deliver US1 to make expiry order-owned.
2. Deliver US2 to replace fingerprint/parent-claim retry behavior with reservation-set comparison.
3. Deliver US3, deploy and drain all old instances, then apply `migrations/0006_order_owned_reservation_expiry.sql`.
4. Regenerate documentation and complete T018-T024 before release.

## Notes

- Every task follows the required checkbox, sequential ID, optional `[P]`, story-label, and exact-path format.
- `reservations` remains the durable correctness record; the Redis REST lease stays a five-second fail-closed coordination layer, not an idempotency store.
- PostgreSQL/Redis manual verification is environment-dependent. Do not represent T024 as complete unless it actually ran with the required disposable database and live Redis REST configuration.
