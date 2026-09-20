---

description: "Implementation tasks for simplifying OpenTelemetry tracing"
---

# Tasks: Simplify OpenTelemetry Tracing

**Input**: Design documents from `/specs/005-simplify-otel-tracing/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Tests are required by FR-015, SC-003 through SC-006, and Constitution Principle V. Write each listed regression task first, confirm it fails for the intended missing behavior, and then implement the paired production task.

**Organization**: Tasks are grouped by user story while preserving the plan's six mandatory, independently green implementation stages. Because User Stories 1-4 are all P1, their phase order follows the required stages: shared budget, shutdown (US4), batching (US2), trace wiring (US3), configuration (US1), then maintainability (US5).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel after its stated prerequisites because it changes different files and has no dependency on another incomplete task in the same parallel group.
- **[Story]**: Maps the task to User Story 1, 2, 3, 4, or 5 from [spec.md](spec.md).
- Every task names the exact repository path it changes or validates.

## Phase 1: Setup (Baseline Evidence)

**Purpose**: Freeze the compatibility, size, and trace evidence that later stages must preserve or explicitly reconcile.

- [X] T001 Record the commit, Go version, green `go test ./... -count=1` baseline, 1,781-line production telemetry count, propagation hashes, and representative database/readiness/reservation trace graphs in `specs/005-simplify-otel-tracing/evidence/baseline.md`

**Checkpoint**: Baseline evidence exists before any production refactor and includes graph edges rather than only span names.

---

## Phase 2: Foundational (Decision Gates and Stage 1 Shared Budget)

**Purpose**: Resolve the documented compatibility conflicts and complete the first independently green mechanical stage.

**Critical**: T002 must record maintainer-approved outcomes before any dependent stage starts; proposed alternatives in the draft plan are not approvals.

- [X] T002 Record approved D1 native-drop observation, D2 trace-parent policy, D3 configuration compatibility, and D4 diagnostic policy and synchronize the resulting requirements in `specs/005-simplify-otel-tracing/spec.md`, `specs/005-simplify-otel-tracing/plan.md`, `specs/005-simplify-otel-tracing/research.md`, and `specs/005-simplify-otel-tracing/contracts/telemetry-contract.md`
- [X] T003 Create the sole 15-second `ServiceShutdownBudget` definition in `internal/lifecycle/budget.go` and replace the duplicate production constants and comparisons in `cmd/server/gin_server.go`, `internal/infrastructure/telemetry/config.go`, and `internal/infrastructure/telemetry/runtime.go`
- [X] T004 Migrate shared-budget references without changing timing semantics in `cmd/server/gin_server_test.go`, `internal/infrastructure/telemetry/config_test.go`, and `internal/infrastructure/telemetry/runtime_test.go`
- [X] T005 Run the Stage 1 focused tests, `go test ./... -count=1`, and the constant-reference audit from `./`, then record the green checkpoint in `specs/005-simplify-otel-tracing/evidence/implementation.md`

**Checkpoint**: One production budget definition remains, current timeout behavior is unchanged, and the complete suite is green before Stage 2.

---

## Phase 3: User Story 4 - Deliver Accepted Telemetry During Bounded Shutdown (Priority: P1)

**Plan stage**: 2. Fix shutdown starvation.

**Goal**: Reserve a positive telemetry delivery window inside the one service deadline even when HTTP drain or dependency cleanup stalls.

**Independent Test**: Queue a completed sampled span, keep an HTTP request open through its drain allocation, stop the server, observe the span at the local receiver, and prove responsive, stalled, repeated, disabled, and early-deadline shutdowns all remain within the common budget.

### Tests for User Story 4

- [X] T006 [P] [US4] Add a slow-drain OTLP capture regression that proves an accepted span reaches the receiver before the common deadline and a second stop does not flush again in `cmd/server/telemetry_integration_test.go`
- [X] T007 [P] [US4] Add channel-driven regressions for drain expiry and active-connection cancellation, stalled export, blocking database close, earlier or canceled parent contexts, disabled telemetry, and single cleanup ownership in `cmd/server/gin_server_test.go`

### Implementation for User Story 4

- [X] T008 [US4] Expose the immutable effective shutdown timeout needed for deadline allocation while preserving disabled and idempotent lifecycle behavior in `internal/infrastructure/telemetry/runtime.go`
- [X] T009 [US4] Compute one total deadline, reserve the telemetry slice, drain and force-close HTTP within its slice, start dependency close without starving telemetry, and wait only to the common deadline in `cmd/server/gin_server.go`
- [X] T010 [US4] Remove unconditional post-stop database closure while retaining cleanup on telemetry or server construction failure in `cmd/main.go`
- [X] T011 [US4] Run the Stage 2 focused and full-suite commands from `./` and record delivery timing, caller-deadline, blocking-close, and idempotency evidence in `specs/005-simplify-otel-tracing/evidence/implementation.md`

**Checkpoint**: User Story 4 is independently demonstrated, and Stage 2 is green before native batching work begins.

---

## Phase 4: User Story 2 - Serve Traffic During Telemetry Overload (Priority: P1)

**Plan stage**: 3. Replace custom capacity management with native batching.

**Entry gate**: T002 must approve D1; a SpanProcessor-only wrapper is not an implementable substitute on the pinned SDK.

**Goal**: Use the native non-blocking batch queue while preserving exact existing drop/failure counters, bounded sanitized warnings, and request independence from telemetry pressure.

**Independent Test**: Hold one batch in flight, fill the complete native waiting queue, finish additional sampled spans, and prove requests finish before release while `/metrics` reports exact bounded outcomes and a stalled warning sink cannot block or create unbounded work.

### Tests for User Story 2

- [X] T012 [P] [US2] Replace gate-mechanics tests with real native-queue saturation, exact sampled-drop accounting, non-blocking request completion, bounded work, intake-stop, and idempotent shutdown regressions in `internal/infrastructure/telemetry/processor_test.go`
- [X] T013 [US2] Add batch-level transport, timeout, server, serialization, unknown and final-flush failure classification plus shared warning-window, slow-sink, and no-double-count regressions in `internal/infrastructure/telemetry/processor_test.go`
- [X] T014 [P] [US2] Extend scrape tests for unchanged metric names, label keys, bounded reason vocabularies, exact queue-drop and batch-failure counts, and absence of SDK-internal public series in `internal/infrastructure/metrics/metrics_test.go`
- [X] T015 [P] [US2] Add serial isolated tests for the D1-approved SDK observation hook, environment/global restoration, concurrent drop filtering, unknown instruments, and sanitized SDK diagnostics in `internal/infrastructure/telemetry/runtime_test.go`

### Implementation for User Story 2

- [X] T016 [US2] Replace `TokenGate`, `DropProcessor`, and `CapacityExporter` with the D1-approved native-drop bridge, small exporter outcome adapter, sanitized marker errors, and one bounded warning worker in `internal/infrastructure/telemetry/processor.go`
- [X] T017 [US2] Construct the native non-blocking batch processor without `WithBlocking`, install approved observation and diagnostic globals before SDK construction, and restore owned process state safely at shutdown in `internal/infrastructure/telemetry/runtime.go`
- [X] T018 [US2] Run the Stage 3 focused suite, native saturation test, obsolete-symbol audit, and `go test ./... -count=1` from `./`, then record exact counts and bounded-work evidence in `specs/005-simplify-otel-tracing/evidence/implementation.md`

**Checkpoint**: User Story 2 is independently green with no custom capacity gate and no new public metric or label.

---

## Phase 5: User Story 3 - Follow the Same Request Across Boundaries (Priority: P1)

**Plan stage**: 4. Remove per-use-case decorators and decouple wire.

**Entry gate**: T002 must approve D2 and synchronize FR-008, FR-009, RC-B, SC-002, the contract, and trace expectations before decorator deletion.

**Goal**: Remove mirrored use-case decorators while preserving the approved HTTP, PostgreSQL, Redis, propagation, status, privacy, and log-correlation topology.

**Independent Test**: Capture equivalent database-backed business, readiness, and reservation requests and compare names, kinds, trace continuity, resource/status attributes, active log IDs, and the exact D2-approved parent edges.

### Tests for User Story 3

- [X] T019 [P] [US3] Replace decorator-based route fixtures with direct use cases and assert the D2-approved representative route and reservation graph in `internal/delivery/http/tracing_integration_test.go`
- [X] T020 [P] [US3] Move retained failure completion, safe status, secret exclusion, and active-span log-correlation assertions out of decorator fixtures in `internal/delivery/http/tracing_security_test.go`
- [X] T021 [P] [US3] Replace the decorated probe fixture with direct use-case wiring while preserving real OTLP request and response assertions in `cmd/server/telemetry_integration_test.go`
- [X] T022 [P] [US3] Assert the D2-approved immediate parents and unchanged names for real pgx and controlled Redis operations in `internal/infrastructure/database/postgres_test.go` and `internal/infrastructure/redis/reservation_locker_test.go`

### Implementation for User Story 3

- [X] T023 [US3] Pass concrete application use cases directly to handlers, remove telemetry/logger imports, and change `NewContainer` to accept only database and reservation dependencies in `internal/wire/container.go`
- [X] T024 [US3] Update container construction for the direct-use-case signature without changing Redis tracing or router contracts in `cmd/server/gin_server.go`
- [X] T025 [US3] Implement the D2-approved application-span policy at explicit delivery call sites, retain the single HTTP `EnrichContext` hook, and preserve coverage/status behavior in `internal/delivery/http/handler/category_handler.go`, `internal/delivery/http/handler/product_handler.go`, `internal/delivery/http/handler/reviewHandler.go`, `internal/delivery/http/handler/inventory_handler.go`, `internal/delivery/http/router.go`, and `internal/delivery/http/middleware/tracing.go`
- [X] T026 [US3] Delete the retired decorator hierarchy and its obsolete tests in `internal/infrastructure/telemetry/decorators.go` and `internal/infrastructure/telemetry/decorators_test.go`, run the Stage 4 focused/full suites and wire-import audit from `./`, and record the before/after graph comparison in `specs/005-simplify-otel-tracing/evidence/implementation.md`

**Checkpoint**: User Story 3 is independently green, wire has no telemetry dependency, and the trace comparison follows the approved D2 policy without flattening graph edges.

---

## Phase 6: User Story 1 - Start the Service Safely (Priority: P1)

**Plan stage**: 5. Collapse owned configuration.

**Entry gate**: T002 must approve D3 and D4; implementation must not silently replace field-level rejection, parent-based 0.10 sampling, or secret-safe diagnostics with SDK defaults.

**Goal**: Give each retained startup gate one validation owner, delegate approved native tuning, preserve safe disabled/enabled startup, and document every accepted compatibility delta.

**Independent Test**: Exercise unset, false, malformed enablement, every invalid owned field, unavailable destination, delegated tuning/default/precedence cases, root ratios zero/one/default, upstream sampling, and secret sentinels before listener binding.

### Tests for User Story 1

- [X] T027 [P] [US1] Rewrite configuration tests around enabled, endpoint, environment fallback, baggage allowlist, shutdown timeout overflow/budget bounds, disabled short-circuiting, and one construction path in `internal/infrastructure/telemetry/config_test.go`
- [X] T028 [P] [US1] Add D3-approved native queue/batch/delay/timeout/compression/header precedence and parent-based root sampling tests, including ratios zero, one, and the 0.10 default, in `internal/infrastructure/telemetry/runtime_test.go`
- [X] T029 [P] [US1] Extend process-startup tests for field-only pre-listener failures, valid unavailable destinations, disabled zero-I/O startup, environment fallback, constructor propagation, and credential-safe errors in `cmd/main_test.go`
- [X] T030 [US1] Add malformed native setting and header sentinel tests proving SDK parser diagnostics are sanitized and never counted as exporter failures in `internal/infrastructure/telemetry/runtime_test.go`

### Implementation for User Story 1

- [X] T031 [US1] Collapse configuration to the five owned gates, reject shutdown overflow before duration conversion, remove tuning parsers and `validateEnabled`, and expose only validated construction in `internal/infrastructure/telemetry/config.go`
- [X] T032 [US1] Delegate approved BSP/exporter tuning and sampling behavior to the SDK, apply only approved absent-setting defaults, and install safe diagnostics before native parsing in `internal/infrastructure/telemetry/runtime.go`
- [X] T033 [US1] Route process startup through the single validated telemetry construction path without adding a second parser in `cmd/main.go`
- [X] T034 [P] [US1] Document the retained gates, native settings, defaults, precedence, approved invalid-value behavior, sampling, and secret-safe diagnostics in `README.md` and `.env.example`
- [X] T035 [US1] Run the Stage 5 focused/full suites and propagation tests from `./`, then record startup, zero-I/O, sampling, precedence, and secret-sentinel evidence in `specs/005-simplify-otel-tracing/evidence/implementation.md`

**Checkpoint**: User Story 1 is independently green with one validation path per field and no unapproved operator-contract change.

---

## Phase 7: User Story 5 - Understand and Extend Tracing Without Mirrored Interfaces (Priority: P2)

**Plan stage**: 6. Housekeeping.

**Goal**: Finish constructor safety, remove obsolete dependencies and deprecated no-op providers, prove new use-case methods need no telemetry edit, and meet the 712-line comprehension target.

**Independent Test**: Reject nil telemetry before route setup, propagate constructor failures through main, add a direct-use-case compile fixture without touching telemetry, measure at most 712 normally formatted production lines, and complete a recorded review session within 60 minutes.

### Tests for User Story 5

- [X] T036 [P] [US5] Add nil-telemetry constructor rejection and pre-route-setup assertions for the error-returning server constructors in `cmd/server/gin_server_test.go`
- [X] T037 [P] [US5] Add server-construction error propagation and startup-failure cleanup ownership regressions in `cmd/main_test.go`
- [X] T038 [P] [US5] Add a direct-use-case composition fixture proving an added uninstrumented method needs no telemetry type or decorator change in `internal/wire/container_test.go`
- [X] T039 [US5] Add debug-enabled endpoint/header/receiver-body secret sentinel and bounded repeated-diagnostic regressions in `internal/infrastructure/telemetry/processor_test.go` and `internal/infrastructure/telemetry/runtime_test.go`

### Implementation for User Story 5

- [X] T040 [US5] Replace deprecated no-op tracer providers, remove the commented stdout exporter path, run `go mod tidy`, and review only the intended dependency diff in `internal/infrastructure/telemetry/runtime.go`, `internal/infrastructure/redis/reservation_locker.go`, `go.mod`, and `go.sum`
- [X] T041 [US5] Make `NewServerWithOptions` and `NewServer` return errors for nil telemetry, preserve the existing unexported concrete type, and update the `AppServer` contract and process caller in `cmd/server/gin_server.go`, `cmd/server/server.go`, and `cmd/main.go`
- [X] T042 [US5] Implement the D4-approved allowlisted debug formatter at the exporter boundary and route it through the bounded warning path without emitting raw error text in `internal/infrastructure/telemetry/processor.go`, `internal/infrastructure/telemetry/runtime.go`, and `internal/infrastructure/logger/logger.go`
- [ ] T043 [US5] Measure the final production telemetry source at no more than 712 physical lines, audit that retired machinery was not relocated, and record the per-file count in `specs/005-simplify-otel-tracing/evidence/implementation.md`
- [X] T044 [US5] Run the Stage 6 focused/full suites and constructor, dependency, obsolete-symbol, and normal-formatting audits from `./`, then record the green checkpoint in `specs/005-simplify-otel-tracing/evidence/implementation.md`

**Checkpoint**: User Story 5 is independently demonstrated, production telemetry is within the size budget, and no parallel domain-interface hierarchy remains.

---

## Phase 8: Polish & Cross-Cutting Acceptance

**Purpose**: Produce the final database, performance, confidentiality, immutability, and comprehension evidence required for acceptance.

- [ ] T045 Re-run seeded database-backed business, readiness, and Redis/PostgreSQL reservation captures and record the normalized final graph comparison in `specs/005-simplify-otel-tracing/evidence/final-traces.md`
- [ ] T046 Run at least ten interleaved warmed tracing-disabled versus bypass benchmark runs, analyze median and p95 at 95% confidence, and record SC-003 results in `specs/005-simplify-otel-tracing/evidence/benchmarks.md`
- [ ] T047 Run the non-skipped PostgreSQL tracing and repository integration suites with an isolated `TEST_DATABASE_URL` from `./` and record database evidence in `specs/005-simplify-otel-tracing/evidence/implementation.md`
- [ ] T048 Run `gofmt`, `go test ./... -count=1`, `go test -race ./...`, `go vet ./...`, `git diff --check`, propagation hash/diff checks, and final scope audits from `./`, then record exact commands and results in `specs/005-simplify-otel-tracing/evidence/implementation.md`
- [ ] T049 Record the reviewer, duration, and explanation of enablement, validation ownership, batching/drop reporting, propagation, sampling, and shutdown from the at-most-60-minute comprehension session in `specs/005-simplify-otel-tracing/evidence/comprehension-review.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 - Setup**: Starts immediately and must finish before production edits.
- **Phase 2 - Foundational / Stage 1**: Depends on T001. T002 is a hard product-contract gate for Stages 3-6; T003-T005 complete the first green code stage.
- **Phase 3 - US4 / Stage 2**: Depends on Phase 2 and must be green before native batching changes.
- **Phase 4 - US2 / Stage 3**: Depends on US4 and the approved D1 outcome in T002.
- **Phase 5 - US3 / Stage 4**: Depends on US2 and the approved, synchronized D2 outcome in T002.
- **Phase 6 - US1 / Stage 5**: Depends on US3 and the approved D3/D4 outcomes in T002.
- **Phase 7 - US5 / Stage 6**: Depends on every P1 story and completes the maintainability goal.
- **Phase 8 - Polish**: Depends on all selected user stories and produces final acceptance evidence.

### User Story Dependency Graph

```text
Baseline
  -> Decision gates + Stage 1 shared budget
       -> US4 bounded shutdown (Stage 2)
            -> US2 native overload handling (Stage 3, D1)
                 -> US3 direct wiring and trace compatibility (Stage 4, D2)
                      -> US1 safe startup and configuration (Stage 5, D3/D4)
                           -> US5 maintainability and housekeeping (Stage 6)
                                -> Final acceptance evidence
```

The P1 stories are ordered by the user's mandatory six-stage sequence, not by story number. No later stage may be used to make an earlier intermediate stage compile or pass.

### Within Each User Story

- Complete the listed test tasks and demonstrate the intended failure before paired production work.
- Migrate all fixtures affected by a stage in that same stage; never commit an uncompilable deletion.
- Run the focused command and `go test ./... -count=1` at every checkpoint before proceeding.
- Keep `internal/infrastructure/telemetry/propagation.go` and `internal/infrastructure/telemetry/propagation_test.go` byte-for-byte unchanged.
- Preserve domain interfaces, HTTP responses, PostgreSQL behavior, Redis fail-closed behavior, Prometheus names/labels, and secret exclusions throughout.

### Parallel Opportunities

- After T005, T006 and T007 can be authored in parallel in separate server test files.
- In US2, T012, T014, and T015 can proceed in parallel; T013 follows T012 because both edit `processor_test.go`.
- In US3, T019-T022 can proceed in parallel in separate test areas before T023-T025 implement the approved topology.
- In US1, T027-T029 can proceed in parallel; T030 follows the runtime-test work in T028.
- In US5, T036-T038 can proceed in parallel before constructor and housekeeping implementation.
- Final trace capture, benchmark execution, and PostgreSQL integration require the completed implementation and should use isolated environments rather than sharing mutable process globals.

---

## Parallel Examples

### User Story 4

```text
Task T006: Slow-drain delivery capture in cmd/server/telemetry_integration_test.go
Task T007: Deadline and blocking-cleanup matrix in cmd/server/gin_server_test.go
```

### User Story 2

```text
Task T012: Native saturation behavior in internal/infrastructure/telemetry/processor_test.go
Task T014: Public scrape contract in internal/infrastructure/metrics/metrics_test.go
Task T015: SDK hook isolation in internal/infrastructure/telemetry/runtime_test.go
```

### User Story 3

```text
Task T019: Representative route graph migration
Task T020: Failure, privacy, and log-correlation migration
Task T021: Server OTLP fixture migration
Task T022: Real pgx and controlled Redis parent checks
```

### User Story 1

```text
Task T027: Five-gate configuration matrix
Task T028: Native tuning and sampling behavior
Task T029: Process startup and listener boundary
```

### User Story 5

```text
Task T036: Nil-runtime server constructor contract
Task T037: Main error propagation and cleanup ownership
Task T038: Direct-use-case extensibility proof
```

---

## Implementation Strategy

### MVP Scope

The deliverable cannot safely stop at User Story 1 alone. All four P1 stories form the compatibility MVP, and the mandated stage order requires completing Phases 1-6: baseline and decisions, shared budget, US4 shutdown, US2 overload, US3 trace continuity, and US1 startup/configuration. Stop after Phase 6 for an independently validated P1 release candidate; add US5 to realize the stated simplification goal.

### Incremental Delivery

1. **Baseline and gates**: Freeze evidence and obtain explicit D1-D4 decisions.
2. **Stage 1**: Establish one shutdown-budget authority with no behavior change.
3. **US4 / Stage 2**: Fix shutdown starvation and prove delivery opportunity.
4. **US2 / Stage 3**: Replace capacity machinery with approved native batching observation.
5. **US3 / Stage 4**: Remove decorators and validate the approved causal trace graph.
6. **US1 / Stage 5**: Collapse configuration ownership without hidden compatibility drift.
7. **US5 / Stage 6**: Finish housekeeping, constructor safety, size reduction, and comprehension proof.
8. **Acceptance**: Run real database, race, vet, benchmark, trace, hash, and review evidence.

### Scope Guardrails

- Do not add Jaeger, an OpenTelemetry Collector deployment, hosted backend, vendor exporter, metrics SDK/export pipeline, or logs export pipeline.
- Do not add OpenTelemetry imports to `internal/domain/` or business implementations under `internal/app/`.
- Do not change public HTTP contracts, handler signatures, PostgreSQL schema/transactions, business rules, or reservation concurrency semantics.
- Do not rename Prometheus metrics or labels, expose SDK-internal series, or emit raw endpoint, header, credential, response-body, SQL, Redis command/key, baggage, or customer data.
- Do not move retired gate, decorator, or parser complexity outside `internal/infrastructure/telemetry` to satisfy the line target.
- Do not modify `internal/infrastructure/telemetry/propagation.go` or `internal/infrastructure/telemetry/propagation_test.go`.

## Notes

- `[P]` marks file-disjoint work that can proceed concurrently only after its phase prerequisites.
- Tests that mutate environment variables or OpenTelemetry process globals must run serially and restore state after runtime shutdown.
- Native BSP queue capacity excludes the held in-flight batch; saturation assertions must account for that semantic difference.
- PostgreSQL integration evidence requires a disposable configured `TEST_DATABASE_URL`; a skipped test is not acceptance evidence.
- Commit after each independently green stage or cohesive test/implementation pair, and stop at every checkpoint before starting the next stage.
