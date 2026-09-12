---

description: "Implementation tasks for end-to-end OpenTelemetry tracing"
---

# Tasks: End-to-End OpenTelemetry Tracing

**Input**: Design documents from `/specs/004-opentelemetry-tracing/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Tests are required by FR-019 and Constitution Principle V. Write each listed test task first and confirm it fails for the intended missing behavior before implementing the paired production task.

**Organization**: Tasks are grouped by user story so each story can be implemented and validated as a distinct increment. Shared runtime work is isolated in Setup and Foundational phases.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel after its stated prerequisites because it touches different files and does not depend on another incomplete task in the same parallel group.
- **[Story]**: Maps the task to User Story 1, 2, or 3 from [spec.md](spec.md).
- Every task names the exact repository path it changes or validates.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add the pinned libraries required by the approved OTLP/HTTP and pgx design.

- [X] T001 Add OpenTelemetry API/SDK/resource/semantic-convention/OTLP-HTTP exporter 1.46.0 and exaring/otelpgx 0.12.0 dependencies in `go.mod` and `go.sum`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Build the configuration, propagation, metrics, queueing, and runtime primitives required by every traced request.

**Critical**: Complete this phase before starting any user-story implementation.

- [X] T002 [P] Add table-driven tests for disabled defaults, every enabled configuration field, bounds, cross-field validation, and secret-safe errors in `internal/infrastructure/telemetry/config_test.go`
- [X] T003 Implement immutable `TelemetryConfig` environment parsing and validation from `data-model.md` in `internal/infrastructure/telemetry/config.go`
- [X] T004 [P] Add W3C trace-context and default-deny baggage allowlist extraction/injection tests in `internal/infrastructure/telemetry/propagation_test.go`
- [X] T005 Implement the W3C trace-context plus filtering baggage propagator in `internal/infrastructure/telemetry/propagation.go`
- [X] T006 [P] Add isolated collector tests for bounded queue-drop and exporter-failure metric labels in `internal/infrastructure/metrics/metrics_test.go`
- [X] T007 Add telemetry drop and exporter failure collectors with bounded label vocabularies in `internal/infrastructure/metrics/metrics.go`
- [X] T008 Add blocking-exporter tests for non-blocking drop-new capacity, exact release accounting, rate-limited warnings, and bounded shutdown in `internal/infrastructure/telemetry/processor_test.go`
- [X] T009 Implement the bounded drop-new span processor and sanitized failure reporting in `internal/infrastructure/telemetry/processor.go`
- [X] T010 Add `TestOTLPHTTPExporter` plus no-op, resource, parent-based sampler, idempotent shutdown, success-response, non-success-response, and unavailable-receiver tests in `internal/infrastructure/telemetry/runtime_test.go`
- [X] T011 Implement the explicit `TelemetryRuntime`, OTLP/HTTP exporter, resource identity, parent-based sampler, global compatibility registration, and bounded lifecycle in `internal/infrastructure/telemetry/runtime.go`

**Checkpoint**: The service has a tested, explicitly constructible telemetry runtime that can export to the local capture receiver without Jaeger or another backend.

---

## Phase 3: User Story 1 - Follow a Request From Entry to Completion (Priority: P1) MVP

**Goal**: Produce one connected trace from Gin entry through the invoked application operation, PostgreSQL, Redis reservation coordination, and the final response.

**Independent Test**: With root sampling set to `1`, exercise representative category, product, review, inventory-reservation, and readiness routes and assert a single causal trace with the contract names, parents, outcomes, and safe bounded attributes; also verify valid upstream continuation, honored upstream non-sampling, and malformed-context fallback.

### Tests for User Story 1

- [X] T012 [P] [US1] Add success-path, route-template, coverage-exclusion, upstream-parent, upstream-unsampled, and malformed-context middleware tests in `internal/delivery/http/middleware/tracing_test.go`
- [X] T013 [P] [US1] Add success-path span-name, parent-context, result-preservation, and interface-conformance tests for all category/product/review/inventory/readiness use-case decorators in `internal/infrastructure/telemetry/decorators_test.go`
- [X] T014 [P] [US1] Add `TEST_DATABASE_URL` integration tests for pgx acquire/query/transaction parenting and SQL/parameter/connection-detail exclusion in `internal/infrastructure/database/postgres_test.go`
- [X] T015 [P] [US1] Extend reservation adapter tests with acquire/release client-span parenting, W3C injection, baggage allowlist, and Redis command/credential exclusion in `internal/infrastructure/redis/reservation_locker_test.go`

### Implementation for User Story 1

- [X] T016 [P] [US1] Implement coverage filtering, upstream extraction, server-span creation, matched-route naming, safe HTTP attributes, and final response closure in `internal/delivery/http/middleware/tracing.go`
- [X] T017 [P] [US1] Implement explicit decorators for every handler-facing domain use-case interface using the `app.<module>.<operation>` vocabulary in `internal/infrastructure/telemetry/decorators.go`
- [X] T018 [P] [US1] Accept an explicit tracer provider and attach safely configured `otelpgx` instrumentation during pool creation in `internal/infrastructure/database/postgres.go`
- [X] T019 [P] [US1] Accept explicit tracing dependencies, create acquire/release client spans, and inject filtered propagation headers in `internal/infrastructure/redis/reservation_locker.go`
- [X] T020 [US1] Wrap every category, product, review, inventory, and readiness use case before handler construction in `internal/wire/container.go`
- [X] T021 [P] [US1] Register tracing before authentication and handlers while preserving `/api/v1` and `/readyz` coverage exclusions in `internal/delivery/http/router.go`
- [X] T022 [US1] Pass the explicit telemetry runtime through Redis, container, and router construction in `cmd/server/gin_server.go`
- [X] T023 [US1] Add representative route-family and reservation hierarchy acceptance tests covering SC-001 and SC-002 in `internal/delivery/http/tracing_integration_test.go`

**Checkpoint**: User Story 1 is complete when successful requests form the documented end-to-end causal hierarchy without domain-layer OpenTelemetry dependencies.

---

## Phase 4: User Story 2 - Diagnose Failures and Slow Operations (Priority: P2)

**Goal**: Make rejected, failed, cancelled, timed-out, panicking, and slow operations diagnosable while correlating safe structured logs to their active spans.

**Independent Test**: Exercise authentication rejection, validation failure, dependency failure, cancellation, deadline expiry, recovered panic, and a slow fake dependency; assert exact HTTP outcomes, correct span status/event behavior, closed spans, matching log `trace_id`/`span_id`, and zero forbidden telemetry values.

### Tests for User Story 2

- [X] T024 [P] [US2] Add 4xx-unset, 5xx-error, cancellation, deadline, client-disconnect, and final-status middleware tests in `internal/delivery/http/middleware/tracing_test.go`
- [X] T025 [P] [US2] Add recovered-panic span closure, safe constant event, and established 500 response tests in `internal/delivery/http/middleware/recovery_test.go`
- [X] T026 [P] [US2] Add active-span log correlation and no-active-span regression tests with an observed zap core in `internal/infrastructure/logger/logger_test.go`
- [X] T027 [P] [US2] Add decorator failure, recovered-child-failure, cancellation, and slow-operation timing tests in `internal/infrastructure/telemetry/decorators_test.go`

### Implementation for User Story 2

- [X] T028 [P] [US2] Implement exact 4xx/5xx/interruption status rules and safe classified events in `internal/delivery/http/middleware/tracing.go`
- [X] T029 [P] [US2] Implement tracing-aware panic recovery that records a constant event, marks the active server span as error, and preserves the current 500 contract in `internal/delivery/http/middleware/recovery.go`
- [X] T030 [P] [US2] Enrich request-scoped loggers from the active context with lowercase `trace_id` and `span_id` while preserving existing fields and causes in `internal/infrastructure/logger/logger.go`
- [X] T031 [US2] Record bounded application outcomes and refresh the contextual logger after each child span starts in `internal/infrastructure/telemetry/decorators.go`
- [X] T032 [US2] Add end-to-end failure, slow-dependency, log-correlation, and forbidden-value scans covering SC-003, SC-004, and SC-007 in `internal/delivery/http/tracing_security_test.go`

**Checkpoint**: User Story 2 is complete when every diagnostic scenario has accurate, closed spans and safely correlated logs without changing client-visible behavior.

---

## Phase 5: User Story 3 - Operate Tracing Safely (Priority: P3)

**Goal**: Support safe enable/disable configuration, successful OTLP delivery, fail-open destination outages, bounded resource use, and graceful shutdown without provisioning a trace backend.

**Independent Test**: Run the service with tracing disabled, with the local OTLP/HTTP capture receiver, with receiver non-success responses, and with an unavailable destination; then saturate the queue and trigger graceful shutdown while asserting unchanged business responses, exact bounded metrics, safe warnings, successful flush when possible, and exit within the 15-second service budget.

### Tests for User Story 3

- [X] T033 [P] [US3] Add process-startup tests for disabled mode, valid enabled mode, invalid enabled configuration, environment fallback, and credential-redacted failures in `cmd/main_test.go`
- [X] T034 [P] [US3] Extend processor tests with concurrent saturation, exporter outage, bounded goroutine/memory behavior, exact drop counts, and warning rate-limit assertions in `internal/infrastructure/telemetry/processor_test.go`
- [X] T035 [P] [US3] Add server lifecycle tests for disabled operation, accepted-request drain, remaining-deadline telemetry flush, exporter timeout, and idempotent shutdown in `cmd/server/gin_server_test.go`
- [X] T036 [P] [US3] Extend metrics tests to verify queue pressure and exporter failures remain distinguishable and scrape safely through the existing registry in `internal/infrastructure/metrics/metrics_test.go`

### Implementation for User Story 3

- [X] T037 [P] [US3] Harden concurrent drop-new accounting, intake stop, exporter error classification, warning rate limiting, and deadline handling in `internal/infrastructure/telemetry/processor.go`
- [X] T038 [P] [US3] Connect runtime export failures and queue drops to the bounded telemetry metrics without exposing endpoints or headers in `internal/infrastructure/telemetry/runtime.go`
- [X] T039 [US3] Initialize validated telemetry before PostgreSQL, fail startup safely on invalid enabled configuration, and preserve the disabled no-op path in `cmd/main.go`
- [X] T040 [US3] Replace hidden signal handling with an injectable lifecycle that stops HTTP intake, drains accepted requests, closes dependencies, and flushes telemetry within one 15-second context in `cmd/server/gin_server.go`
- [X] T041 [US3] Add enabled, disabled, local-capture-receiver, unavailable-destination, queue-pressure, and graceful-shutdown acceptance coverage for SC-006 and SC-009 in `cmd/server/telemetry_integration_test.go`

**Checkpoint**: User Story 3 is complete when exporter success and failure behavior is deterministic, bounded, secret-safe, and independent of Jaeger or another installed backend.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Finish operator guidance, performance evidence, and repository-wide verification.

- [X] T042 [P] Document every supported tracing variable with disabled safe defaults and placeholder-only values in `.env.example`
- [X] T043 [P] Document coverage, OTLP configuration, sampling, failure behavior, backend exclusions, and the capture-receiver verification command in `README.md`
- [X] T044 Add tracing-enabled versus disabled latency plus bounded allocation/goroutine benchmarks for SC-005 in `internal/delivery/http/tracing_benchmark_test.go`
- [X] T045 Run `gofmt` on changed Go files and execute the focused quickstart commands, `go test ./... -count=1`, `go test -race ./...`, `go vet ./...`, and `git diff --check` from repository root `./`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 - Setup**: No dependencies; starts immediately.
- **Phase 2 - Foundational**: Depends on T001 and blocks every user story.
- **Phase 3 - User Story 1**: Depends on Phase 2 and delivers the MVP trace hierarchy.
- **Phase 4 - User Story 2**: Depends on User Story 1 span creation; its tests and hardening remain independently runnable after that prerequisite.
- **Phase 5 - User Story 3**: Runtime robustness tests can start after Phase 2; server lifecycle integration depends on T022, and final acceptance depends on the selected P1/P2 behavior being present.
- **Phase 6 - Polish**: Depends on all user stories selected for delivery.

### User Story Dependency Graph

```text
Setup
  -> Foundational runtime
       -> US1 complete trace hierarchy (MVP)
            -> US2 failure diagnosis and log correlation
            -> US3 server lifecycle integration
       -> US3 runtime resilience tests
US1 + US2 + US3
  -> Polish and full verification
```

### Within Each User Story

- Complete and observe failure of the listed test tasks before their paired implementation tasks.
- Establish span contracts before wiring composition boundaries.
- Complete core instrumentation before end-to-end acceptance tests.
- Preserve existing domain interfaces, HTTP contracts, database guarantees, and Redis fail-closed behavior throughout.

### Parallel Opportunities

- In Phase 2, configuration, propagation, and metrics test-first pairs can progress independently after T001; processor/runtime work follows their local prerequisites.
- In User Story 1, T012-T015 can run in parallel, followed by parallel implementation in T016-T019; T020-T023 integrate those results.
- In User Story 2, T024-T027 can run in parallel, then T028-T030 can run in parallel before decorator and acceptance integration.
- In User Story 3, T033-T036 can run in parallel; T037 and T038 can proceed in parallel before lifecycle composition.
- T042 and T043 can run in parallel after implementation behavior and configuration are stable.

---

## Parallel Examples

### User Story 1

```text
Task T012: Gin success, route, and upstream-context tests
Task T013: Application decorator hierarchy tests
Task T014: pgx integration tracing tests
Task T015: Redis span and propagation tests
```

### User Story 2

```text
Task T024: HTTP outcome and interruption tests
Task T025: Panic recovery tests
Task T026: Log correlation tests
Task T027: Decorator failure and timing tests
```

### User Story 3

```text
Task T033: Process startup mode and redaction tests
Task T034: Queue saturation and outage tests
Task T035: Server lifecycle tests
Task T036: Telemetry metrics tests
```

---

## Implementation Strategy

### MVP First: User Story 1

1. Complete Phase 1 dependency setup.
2. Complete Phase 2 and verify `TestOTLPHTTPExporter` against the temporary capture receiver.
3. Complete Phase 3 from contract tests through representative route acceptance.
4. Stop and validate the P1 hierarchy independently before adding diagnostic and resilience behavior.

### Incremental Delivery

1. **Foundation**: Validated runtime, OTLP exporter, safe propagation, bounded processor, and metrics.
2. **MVP / US1**: Connected successful traces across HTTP, application, PostgreSQL, and Redis.
3. **US2**: Accurate failure/interruption status, panic recovery, log correlation, and privacy enforcement.
4. **US3**: Fail-open outages, queue saturation, disabled startup, bounded flush, and lifecycle acceptance.
5. **Polish**: Operator documentation, performance evidence, and all repository quality gates.

### Scope Guardrails

- Do not add Jaeger, an OpenTelemetry Collector, backend storage, trace UI, Docker Compose service, or vendor exporter.
- Do not add OpenTelemetry imports to `internal/domain/` or business implementations under `internal/app/`.
- Do not change public HTTP response shapes, PostgreSQL schema, transactions, or reservation consistency rules.
- Do not export raw paths, identifiers, payloads, SQL, Redis commands, baggage values, endpoints, headers, or credentials.

## Notes

- `[P]` marks file-disjoint work that can proceed concurrently after prerequisites.
- `[US1]`, `[US2]`, and `[US3]` provide direct traceability to the specification.
- PostgreSQL integration tasks require a disposable `TEST_DATABASE_URL`; all other exporter verification is backend-neutral.
- Commit after each task or cohesive test/implementation pair, and stop at each checkpoint for independent validation.
