---

description: "Implementation tasks for checkout inventory reservation"
---

# Tasks: Checkout Inventory Reservation

**Input**: Design documents from `/specs/001-checkout-inventory-reservation/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/create-reservation.openapi.yaml`, and `quickstart.md`

**Tests**: Required by the feature specification, quickstart, and repository constitution. Write each listed test before its corresponding implementation task and confirm it fails for the missing behavior.

**Organization**: Tasks are grouped by user story so each increment is independently implementable and testable.

## Path Conventions

- Go delivery, application, domain, infrastructure, and composition code lives under `internal/`; server configuration is under `cmd/`.
- Versioned PostgreSQL changes live in `migrations/`; public API documentation is generated in `docs/`.
- Feature-specific design and acceptance instructions live in `specs/001-checkout-inventory-reservation/`.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Document the feature's Redis REST configuration before composing the new dependency.

- [ ] T001 [P] Document `REDIS_REST_URL` and `REDIS_REST_TOKEN` as required no-secret checkout-reservation configuration in `.env.example` and `README.md`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Establish persistence, domain contracts, and cross-instance coordination required by every reservation scenario.

**⚠️ CRITICAL**: Complete this phase before starting user-story implementation.

- [ ] T002 Create the forward-only checkout reservation integrity migration, including request claims, reservation/stock-move links, quantity and expiry checks, and the single-default-location constraint in `migrations/0005_checkout_reservation_integrity.sql`
- [ ] T003 Add isolated PostgreSQL migration/invariant coverage for `0005_checkout_reservation_integrity.sql` in `internal/infrastructure/repository/inventoryRepo_test.go`
- [ ] T004 [P] Extend reservation domain types, stable errors, `ReservationRepository`, and `ReservationLocker` ports in `internal/domain/inventory.go`
- [ ] T005 [P] Add PostgreSQL row models and domain mapping for reservation request claims, reservations, levels, and linked stock moves in `internal/infrastructure/repository/model/reservation_model.go`
- [ ] T006 [P] Implement the fail-closed Upstash Redis REST Lua lease acquire/release adapter with a fixed five-second `PX 5000` TTL, owner-token checks, bounded contexts, and no-secret errors in `internal/infrastructure/redis/reservation_locker.go`
- [ ] T007 [P] Add REST-adapter tests for the five-second lease TTL, atomic acquire, contention, token-checked release, malformed responses, and transport failure in `internal/infrastructure/redis/reservation_locker_test.go`

**Checkpoint**: Schema invariants and narrow domain/infrastructure boundaries exist; user-story work can proceed.

---

## Phase 3: User Story 1 - Reserve Checkout Stock (Priority: P1) 🎯 MVP

**Goal**: An order service can reserve sufficient stock for all unique requested variants at the configured default location and receive the active holds.

**Independent Test**: Submit a valid multi-item `POST /api/v1/inventory/reservations` request against seeded tracked variants and a default location; receive one `ACTIVE` reservation per item with a 60-minute expiry, while each level changes available `-qty` and reserved `+qty` and each linked `RESERVE` move exists.

### Tests for User Story 1

- [ ] T008 [P] [US1] Add domain tests for positive reservation request items, duplicate-variant rejection, and available-to-reserved transfer invariants in `internal/domain/inventory_test.go`
- [ ] T009 [P] [US1] Add use-case success-path tests with fakes for multi-item canonical input, default-location selection, and 60-minute expiry in `internal/app/inventory/reservationUseCase_test.go`
- [ ] T010 [P] [US1] Add PostgreSQL integration coverage for one transactional successful multi-item reservation, level updates, and linked `RESERVE` moves in `internal/infrastructure/repository/inventoryRepo_test.go`
- [ ] T011 [P] [US1] Add Gin contract coverage for the documented 201 create response and returned reservation fields in `internal/delivery/http/handler/inventory_handler_test.go`

### Implementation for User Story 1

- [ ] T012 [P] [US1] Add `orderId`/`items[].id`/`items[].qty` request and success-response DTOs with strict binding validation in `internal/delivery/http/dto/inventory_dto.go`
- [ ] T013 [US1] Implement the create-reservation use case to validate input, generate UUIDv7 reservation IDs, set database-relative 60-minute expiry intent, and orchestrate the repository in `internal/app/inventory/reservationUseCase.go`
- [ ] T014 [US1] Implement the pgx/v5 transactional happy path that resolves the default location and tracked variants, locks levels in inventory-item order, transfers quantities, and inserts reservations and linked moves in `internal/infrastructure/repository/inventoryRepo.go`
- [ ] T015 [US1] Implement the Gin inventory handler with request binding and successful reservation encoding in `internal/delivery/http/handler/inventory_handler.go`
- [ ] T016 [US1] Compose the inventory repository, use case, handler, and fail-closed Redis-locker configuration in `internal/wire/container.go` and `cmd/server/gin_server.go`
- [ ] T017 [US1] Register `POST /api/v1/inventory/reservations` in the existing API group in `internal/delivery/http/router.go`
- [ ] T018 [US1] Add Swagger annotations for the reservation request and 201 response in `internal/delivery/http/handler/inventory_handler.go` and regenerate `docs/docs.go`, `docs/swagger.json`, and `docs/swagger.yaml`

**Checkpoint**: A sufficiently stocked multi-item checkout creates and returns its complete set of active holds.

---

## Phase 4: User Story 2 - Reject an Unfulfillable Checkout Atomically (Priority: P1)

**Goal**: Invalid or unfulfillable checkout requests return stable, safe failures without retaining any partial reservation, move, request claim, or inventory change.

**Independent Test**: Submit an order containing one stocked variant and one insufficient, unknown, untracked, or missing-level variant; receive the documented error identifying only the public failing variant and verify all persistence state is unchanged.

### Tests for User Story 2

- [ ] T019 [P] [US2] Add use-case tests for malformed IDs, empty/duplicate items, non-positive quantities, and missing default-location or non-reservable-variant errors in `internal/app/inventory/reservationUseCase_test.go`
- [ ] T020 [P] [US2] Add PostgreSQL integration tests proving insufficient stock and unresolved inventory roll back every level, request claim, reservation, and stock-move write in `internal/infrastructure/repository/inventoryRepo_test.go`
- [ ] T021 [P] [US2] Add Gin contract tests for stable 400, 404, 409, and 422 error codes and public `variantId` details only in `internal/delivery/http/handler/inventory_handler_test.go`

### Implementation for User Story 2

- [ ] T022 [US2] Extend validation and domain-error classification for invalid, missing, non-reservable, and insufficient reservation inputs in `internal/app/inventory/reservationUseCase.go` and `internal/domain/inventory.go`
- [ ] T023 [US2] Add repository resolution checks and rollback-safe failure paths before any level update, ensuring all quantity checks occur while rows are locked in `internal/infrastructure/repository/inventoryRepo.go`
- [ ] T024 [US2] Map reservation failures to the documented safe HTTP status/code/body shapes, including the public failing `variantId`, in `internal/delivery/http/handler/inventory_handler.go`

**Checkpoint**: Every rejected checkout is auditable through its response but leaves no partial durable inventory effect.

---

## Phase 5: User Story 3 - Keep Competing Checkouts Consistent (Priority: P1)

**Goal**: Concurrent checkout requests cannot over-reserve inventory, and repeated order submissions are durably idempotent.

**Independent Test**: Issue at least 100 concurrent competing requests for finite inventory and verify successful totals do not exceed the starting availability, stored quantities stay non-negative, equal `orderId` retries return original IDs without writes, and changed retries conflict without writes.

### Tests for User Story 3

- [ ] T025 [P] [US3] Add use-case tests for deterministic request fingerprints, canonical lock-key ordering, the fixed five-second lease, matching retry returns, changed retry conflicts, and lease-acquisition failure in `internal/app/inventory/reservationUseCase_test.go`
- [ ] T026 [P] [US3] Add PostgreSQL integration tests for durable order claims, matching/different `orderId` retries, deterministic `FOR UPDATE` locking, and at least 100 competing reservation requests in `internal/infrastructure/repository/inventoryRepo_test.go`
- [ ] T027 [P] [US3] Add Gin contract tests for 200 identical retries, 409 changed-order conflicts, and 503 coordination failures with no leaked configuration data in `internal/delivery/http/handler/inventory_handler_test.go`

### Implementation for User Story 3

- [ ] T028 [US3] Implement SHA-256 fingerprinting of sorted variant/quantity pairs and canonical order-plus-variant lease keys in `internal/app/inventory/reservationUseCase.go`
- [ ] T029 [US3] Integrate fixed five-second lease acquisition before opening a transaction and token-checked best-effort release after every outcome, with fail-closed coordination behavior, in `internal/app/inventory/reservationUseCase.go`
- [ ] T030 [US3] Implement transactional order-claim locking, persisted-result reads for equal fingerprints, conflict handling for changed fingerprints, and deterministic level locking in `internal/infrastructure/repository/inventoryRepo.go`
- [ ] T031 [US3] Return 200 only for persisted identical retries and map in-progress/coordination outcomes without exposing internals in `internal/delivery/http/handler/inventory_handler.go`
- [ ] T032 [US3] Add structured reservation outcome events and Prometheus attempt/outcome/latency metrics without Redis secrets in `internal/app/inventory/reservationUseCase.go` and `internal/infrastructure/metrics/metrics.go`

**Checkpoint**: Cross-instance coordination, database locking, and durable idempotency prevent overselling and duplicate holds.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Complete documentation and verify the delivered API, transaction behavior, and observability.

- [ ] T033 [P] Update setup, request, idempotency, atomic-failure, and Redis-failure validation instructions in `specs/001-checkout-inventory-reservation/quickstart.md` and `README.md`
- [ ] T034 [P] Reconcile the generated Swagger API with the source contract at `specs/001-checkout-inventory-reservation/contracts/create-reservation.openapi.yaml` and `docs/swagger.yaml`
- [ ] T035 Run the feature test commands and disposable-database concurrency checks documented in `specs/001-checkout-inventory-reservation/quickstart.md`
- [ ] T036 Run repository formatting, static analysis, full tests, and generated-document verification for the touched Go and API files from `go.mod`, `cmd/main.go`, and `docs/docs.go`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: Starts immediately.
- **Foundational (Phase 2)**: Starts immediately; T003 follows T002, while T004–T007 can proceed in parallel where file ownership permits. It blocks all user stories.
- **User Story 1 (Phase 3)**: Starts after Phase 2 and delivers the MVP happy path.
- **User Story 2 (Phase 4)**: Starts after Phase 2; it extends the same use case/repository/handler as US1, so schedule it after US1 when a single team owns those files.
- **User Story 3 (Phase 5)**: Starts after Phase 2; it shares the use case/repository/handler with US1 and US2, so schedule it after them unless separate changes are carefully coordinated.
- **Polish (Phase 6)**: Follows the desired story phases.

### User Story Dependencies

- **US1 (P1)**: Depends on Phase 2 only; this is the MVP.
- **US2 (P1)**: Depends on Phase 2 and the US1 reservation transaction/HTTP path to add atomic rejection behavior.
- **US3 (P1)**: Depends on Phase 2 and the US1 persistence path; its retry/coordination work must follow the resulting success and error contracts.

### Within Each User Story

- Write and run the listed tests first.
- Complete domain/application behavior before repository persistence, then delivery/wiring and generated documentation.
- Do not move to the story checkpoint until its independent test criteria pass.

## Parallel Opportunities

### Foundational phase

```text
T004: Domain ports and errors in internal/domain/inventory.go
T005: Row mapping in internal/infrastructure/repository/model/reservation_model.go
T006: Redis REST adapter in internal/infrastructure/redis/reservation_locker.go
```

These tasks touch separate files; T007 follows T006, and T002/T003 retain their migration dependency.

### User Story 1

```text
T008: Domain tests in internal/domain/inventory_test.go
T009: Use-case tests in internal/app/inventory/reservationUseCase_test.go
T010: PostgreSQL integration tests in internal/infrastructure/repository/inventoryRepo_test.go
T011: HTTP contract tests in internal/delivery/http/handler/inventory_handler_test.go
T012: DTO definitions in internal/delivery/http/dto/inventory_dto.go
```

The test files and DTO are independent; serialize subsequent changes to the shared use-case, repository, handler, wire, and router files.

### User Story 2

```text
T019: Use-case failure tests in internal/app/inventory/reservationUseCase_test.go
T020: PostgreSQL rollback tests in internal/infrastructure/repository/inventoryRepo_test.go
T021: HTTP error-contract tests in internal/delivery/http/handler/inventory_handler_test.go
```

### User Story 3

```text
T025: Use-case idempotency tests in internal/app/inventory/reservationUseCase_test.go
T026: PostgreSQL concurrency tests in internal/infrastructure/repository/inventoryRepo_test.go
T027: HTTP retry/coordination tests in internal/delivery/http/handler/inventory_handler_test.go
```

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1 and the blocking Phase 2 foundations.
2. Complete all US1 tests and T012–T018.
3. Run the US1 independent test against the disposable PostgreSQL/Redis environment.
4. Demo the valid multi-item hold before taking on failure and concurrency refinements.

### Incremental Delivery

1. Deliver US1: successful atomic checkout holds.
2. Deliver US2: validation and atomic reject/no-write guarantees.
3. Deliver US3: idempotency and cross-instance safety.
4. Finish documentation and full verification in Phase 6.

## Notes

- Reservation expiry release, cancellation, and consumption are deliberately out of scope; this feature persists the audit state needed for that future lifecycle work.
- PostgreSQL remains the durable correctness authority. Redis coordination failure or contention must fail closed and must never start an inventory transaction.
- Every task above uses the required checklist form: checkbox, sequential task ID, a `[P]` marker only when parallel, a user-story label only for story work, and explicit file path(s).
