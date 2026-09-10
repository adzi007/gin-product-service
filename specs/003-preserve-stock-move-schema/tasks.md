---
description: "Dependency-ordered task list for preserving the stock_moves schema"
---

# Tasks: Preserve Stock Move Schema

**Input**: Design documents from `/specs/003-preserve-stock-move-schema/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [quickstart.md](quickstart.md)

**Tests**: TDD is not requested; this is a verification-and-sweep cleanup with no behavior change. One guard task (T008) retains and extends the existing schema-preservation test per the repository constitution (Principle V).

**Organization**: Tasks are grouped by user story so each story is independently verifiable.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Every task includes the exact file path(s) it touches

## Path Conventions

- Schema artifacts are `db.dbml` and `db.dbdiagram`; versioned DDL is `migrations/*.sql`.
- Go code lives under `internal/`; this feature's docs live under `specs/003-preserve-stock-move-schema/`.
- Prior feature docs live under `specs/001-checkout-inventory-reservation/` and `specs/002-order-owned-expiry/`.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Pin the authoritative schema baseline before sweeping.

- [x] T001 [P] Confirm `specs/003-preserve-stock-move-schema/data-model.md` records the canonical `stock_moves` shape (nine columns: `id`, `inventory_item_id`, `from_location_id`, `to_location_id`, `move_type`, `quantity`, `created_by`, `reason`, `created_at`; no `reservation_id`) exactly as supplied in the feature request

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Pin the database shape before sweeping code and documentation.

**⚠️ CRITICAL**: Complete this phase before the user-story sweeps begin.

- [x] T002 [P] Verify every file under `migrations/` (`0001`–`0006`) contains no statement that adds or requires a `reservation_id` column on `stock_moves`; remove or correct any such statement

**Checkpoint**: Schema baseline and migration set are confirmed clean.

---

## Phase 3: User Story 1 - Keep the Stock Movement Schema Free of Reservation Identity (Priority: P1) 🎯 MVP

**Goal**: The `stock_moves` table is defined with exactly the canonical columns and no `reservation_id`.

**Independent Test**: `grep -n "reservation_id" db.dbml db.dbdiagram` returns no matches, and `db.dbml` defines the nine canonical columns for `stock_moves`. (`db.dbdiagram` is an 87-line layout/viewport companion holding table positions only, so it carries no column definitions; its check is the absence of any `reservation_id` reference.)

### Implementation for User Story 1

- [x] T003 [US1] Verify `db.dbml` defines `stock_moves` with exactly the nine canonical columns and no `reservation_id`, and that `db.dbdiagram` references no `reservation_id`; correct any drift

**Checkpoint**: The schema source of truth conforms to the canonical definition.

---

## Phase 4: User Story 2 - Record Reservation Movements With Existing Columns Only (Priority: P1)

**Goal**: No code path writes a reservation identifier into a stock movement; the `RESERVE` insert uses the existing columns only.

**Independent Test**: `grep -rn "reservation_id" internal/` returns no `stock_moves` column reference, and a created checkout reservation records exactly one `RESERVE` movement per held item with no reservation identifier.

### Implementation for User Story 2

- [x] T004 [P] [US2] Verify the `StockMove` entity in `internal/domain/inventory.go` has no reservation-identifier field
- [x] T005 [P] [US2] Verify the `StockMove` row mapper in `internal/infrastructure/repository/model/reservation_model.go` has no reservation-identifier field
- [x] T006 [P] [US2] Verify the `RESERVE` insert in `internal/infrastructure/repository/inventoryRepo.go` writes only `id`, `inventory_item_id`, `from_location_id`, `to_location_id`, `move_type`, `quantity` and no `reservation_id`
- [x] T007 [P] [US2] Verify the `ADJUST` stock-movement inserts in `internal/infrastructure/repository/productRepo.go` write only the existing columns
- [x] T008 [P] [US2] Confirm the schema-preservation guard in `internal/infrastructure/repository/inventoryRepo_test.go` rejects any `ALTER TABLE stock_moves`; extend it in `internal/infrastructure/repository/inventoryRepo_test.go` to also fail if a `reservation_id` column reference appears on `stock_moves`

**Checkpoint**: Code conforms — reservation identity stays on `reservations`, movements use existing columns.

---

## Phase 5: User Story 3 - Remove Code and Plan Documentation References (Priority: P1)

**Goal**: No plan/spec/task document instructs inserting a `reservation_id` into `stock_moves`.

**Independent Test**: `grep -rn "reservation_id" specs/001-checkout-inventory-reservation specs/002-order-owned-expiry` returns no instruction to insert the column into `stock_moves`.

### Implementation for User Story 3

- [x] T009 [P] [US3] Verify `specs/001-checkout-inventory-reservation/` documents (`spec.md`, `plan.md`, `tasks.md`, `data-model.md`, `quickstart.md`) contain no instruction to insert a `reservation_id` into `stock_moves`; remove or correct any that do
- [x] T010 [P] [US3] Verify `specs/002-order-owned-expiry/` documents contain no such instruction; remove or correct any that do
- [x] T011 [P] [US3] Verify `specs/inventory-reservation.md` (legacy draft) — remove or correct only a `reservation_id` column reference on `stock_moves`, leaving reservation response/identity references intact

**Checkpoint**: All planning documentation is consistent with the canonical schema.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Full verification after the sweep.

- [x] T012 Run `go test ./...`, `go vet ./...`, and `git diff --check`; confirm all pass
- [x] T013 Execute the validation scenarios in `specs/003-preserve-stock-move-schema/quickstart.md` (schema, migration, code, and documentation sweeps) and confirm every expected outcome

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: T001 starts immediately; it pins the baseline.
- **Foundational (Phase 2)**: T002 starts immediately; it blocks the user-story sweeps only in the sense that any found migration drift must be corrected first.
- **User Story 1 (Phase 3)**: Starts after Phase 2; delivers schema-source-of-truth conformance.
- **User Story 2 (Phase 4)**: Starts after Phase 2; independent of US1.
- **User Story 3 (Phase 5)**: Starts after Phase 2; independent of US1 and US2.
- **Polish (Phase 6)**: Follows all desired story phases.

### User Story Dependencies

- **US1 (P1)**: Depends on Phase 2 only; this is the MVP (schema conformance).
- **US2 (P1)**: Depends on Phase 2 only; code sweep is independent of schema/doc sweeps.
- **US3 (P1)**: Depends on Phase 2 only; documentation sweep is independent of the others.

All three stories are independently verifiable and can proceed in parallel after Phase 2.

### Within Each User Story

- Run the story's `Independent Test` sweep first to establish the current state.
- Correct any drift found; when none is found, record the verification.
- Do not move past a story checkpoint until its independent test criteria pass.

### Parallel Opportunities

- T001 and T002 touch different files and can run in parallel.
- T004–T008 touch distinct files (`domain/inventory.go`, `model/reservation_model.go`, `inventoryRepo.go`, `productRepo.go`, `inventoryRepo_test.go`) and can run in parallel.
- T009–T011 touch distinct documentation directories/files and can run in parallel.
- US1, US2, and US3 are mutually independent after Phase 2.

## Parallel Example: User Story 2

```text
Task: "Verify the StockMove entity in internal/domain/inventory.go has no reservation-identifier field"
Task: "Verify the StockMove row mapper in internal/infrastructure/repository/model/reservation_model.go has no reservation-identifier field"
Task: "Verify the RESERVE insert in internal/infrastructure/repository/inventoryRepo.go writes only the existing columns"
Task: "Verify the ADJUST inserts in internal/infrastructure/repository/productRepo.go write only the existing columns"
```

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1 (T001) and Phase 2 (T002).
2. Complete Phase 3 (T003) to confirm the schema source of truth.
3. STOP and VALIDATE: run US1's independent test (`grep` over `db.dbml` / `db.dbdiagram`).

### Incremental Delivery

1. Complete Setup + Foundational → baseline and migration set confirmed clean.
2. Deliver US1 → schema source of truth conforms.
3. Deliver US2 → code sweep confirms no `reservation_id` write into `stock_moves`.
4. Deliver US3 → documentation sweep confirms no stale instruction.
5. Finish Phase 6 verification (`go test ./...`, `go vet ./...`, `git diff --check`, quickstart scenarios).

### Notes

- This is a behavior-preserving cleanup: no migration, no contract change, no new code.
- Each task is a verification-and-correct pass; when the target file is already conformant, the task outcome is "verified, no change".
- After any correction, re-run the relevant independent test and the full suite (T012) before completing the phase.
