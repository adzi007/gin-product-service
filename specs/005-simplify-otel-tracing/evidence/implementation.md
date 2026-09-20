# Implementation Evidence

Records per-stage checkpoints and decision outcomes.

## T002 — Recorded decisions

- D1: **Adopt the experimental observability bridge** plus a small exporter outcome wrapper. `TokenGate`, `DropProcessor`, and `CapacityExporter` are retired; Stages 3-6 and Polish are unblocked.
- D2: **Remove `app.*` spans** (DB/Redis reparent to HTTP; app logs carry HTTP span IDs).
- D3: **Five owned startup gates + SDK delegation**.
- D4: **Allowlisted/redacting debug formatter**.

See `spec.md`, `plan.md`, `research.md`, and `contracts/telemetry-contract.md` for the synchronized wording.

## Stage 1 — Shared shutdown budget (T003-T005)

- Created `internal/lifecycle/budget.go` with the sole `ServiceShutdownBudget = 15 * time.Second`.
- Replaced the duplicate definitions/usages in `cmd/server/gin_server.go`,
  `internal/infrastructure/telemetry/config.go`, and
  `internal/infrastructure/telemetry/runtime.go` with `lifecycle.ServiceShutdownBudget`.
- Constant-reference audit: one authoritative production definition in
  `internal/lifecycle`; all other references import it. No `telemetry.ServiceShutdownBudget`
  or `server.ServiceShutdownBudget` symbol remains.
- Test migration (T004): no test file referenced the constant; no timing semantics changed.
- Focused tests: `go test ./cmd/server ./internal/infrastructure/telemetry -count=1` → ok.
- Full suite: `go test ./... -count=1` → all green.

## Stage 2 — Shutdown starvation fix (T006-T011)

- **T008**: Added `TelemetryRuntime.EffectiveShutdownTimeout()` returning the
  reserved delivery window (zero when disabled).
- **T009**: Rewrote `ginServer.Stop` to compute one total deadline, reserve the
  telemetry slice, drain HTTP within `total − reservation`, force-close active
  connections on drain expiry, close dependencies concurrently without starving
  the flush, flush with the live total context (never the expired drain context),
  and wait for dependency close only until the common deadline.
- **T010**: Removed the unconditional `defer db.Close()` in `cmd/main.go`; the
  server's bounded shutdown sequence is now the single cleanup owner.
- **TDD evidence**: new regressions failed before the fix and pass after:
  - `TestServerSlowDrainStillDeliversAcceptedTelemetry` — FAIL → PASS (queued span
    reaches the receiver despite a slow drain; second Stop does not flush again).
  - `TestServerStopCancelsActiveConnectionsOnDrainExpiry` — FAIL → PASS.
  - `TestServerStopDoesNotStarveTelemetryBehindBlockingDatabaseClose` — FAIL → PASS.
  - `TestServerStopHonorsEarlierCallerDeadline` — PASS (guard, already honored).
  - `TestServerStopClosesDatabaseOnce` — PASS (guard, already idempotent).
- Focused: `go test ./cmd/server -count=1 -v` → all 15 server tests pass.
- Full suite: `go test ./... -count=1` → all green.
- `go vet ./...` → clean; `git diff --check` → clean.

## Stage 3 — Native batching with the D1 drop bridge (T012-T018)

- **T016/T017**: Deleted `TokenGate`, `DropProcessor`, `CapacityExporter`, and
  `DropProcessorConfig` from `processor.go`. Added the D1-approved
  `DropObservationMeterProvider` (embeds the public no-op metric API; intercepts
  `otel.sdk.processor.span.processed` and counts `error.type=queue_full` +
  `otel.component.type=batching_span_processor` additions on the existing
  Prometheus queue_full counter), `OutcomeExporter` (classifies/counts/warns each
  failed batch once and returns a sanitized `*ExportError` marker), and a bounded
  `WarningDispatcher` (one worker, one shared limiter, non-blocking handoff).
  `runtime.go` now builds the native BSP without `WithBlocking`, installs the
  bridge and `OTEL_GO_X_OBSERVABILITY=true` before SDK construction, and restores
  the previous global meter provider and feature flag at shutdown. The global
  error handler recognizes the marker error and never recounts it.
- **T012-T015**: `processor_test.go` rewritten around native saturation
  (`TestNativeBatchProcessorCountsDropsExactly`, `...OnEndIsNonBlocking`,
  `...ExporterOutageNeverBlocksRequests`, `...KeepsGoroutinesBounded`), outcome
  classification/sanitization, warning rate-limiting, slow-sink non-blocking, and
  no-double-count; `runtime_test.go` gained global-state restoration and
  concurrent bridge-counting tests; `metrics_test.go` gained the no-SDK-internal-
  series assertion.
- Obsolete-symbol audit: no `TokenGate`, `DropProcessor`, `CapacityExporter`, or
  `WithBlocking` reference remains under `cmd` or `internal`.
- Focused: `go test ./internal/infrastructure/telemetry/ ./internal/infrastructure/metrics/ -count=1` → ok.
- Full suite: `go test ./... -count=1` → all green.

## Stage 4 — Decorator removal and direct wiring (T019-T026)

- **T023/T024**: `wire.NewContainer(db, reservationLocker)` no longer accepts a
  telemetry runtime; use cases are passed directly to handlers. `gin_server.go`
  calls the new signature. Wire imports neither `telemetry` nor `logger`.
- **T025**: D2 removal policy — no application span is created; the HTTP
  middleware's single `EnrichContext` hook (already present) carries the server
  span ID into request-scoped logs, and database/Redis spans become immediate
  HTTP children.
- **T026**: Deleted `internal/infrastructure/telemetry/decorators.go` and
  `decorators_test.go`.
- **T019-T022**: Migrated `tracing_integration_test.go` (dependency spans parent
  directly under the HTTP span; no `app.*` span), `tracing_security_test.go`
  (direct use cases; server span carries outcome/duration), and
  `cmd/server/telemetry_integration_test.go` (direct probe); real pgx and Redis
  parent fixtures renamed to the D2 HTTP-parent vocabulary.
- Wire-import audit: `internal/wire` imports no telemetry or logger package.
- Full suite: `go test ./... -count=1` → all green.

## Stage 6 — Housekeeping (T036-T044)

- **T040**: Replaced `trace.NewNoopTracerProvider()` with `trace/noop.NewTracerProvider()`
  in `runtime.go` and `redis/reservation_locker.go`; the commented stdout exporter
  block was already removed. `go mod tidy` removed the unused
  `go.opentelemetry.io/otel/exporters/stdout/stdouttrace` dependency and promoted
  `github.com/go-logr/logr` and `go.opentelemetry.io/otel/metric` to direct deps.
- **T041**: `NewServerWithOptions` returns `(*ginServer, error)` and rejects a nil
  telemetry runtime before route setup; `NewServer` returns `(AppServer, error)`
  and propagates the error. All 15 server-test call sites migrated to a
  `mustServer` helper.
- **T042**: Added `debugDiagnostic` (bounded reason only, never raw error text) and
  routed it through the bounded warning path; the production warning sink now
  logs sanitized warnings via the structured logger.
- **T036-T039**: added nil-telemetry rejection, constructor error propagation, a
  direct-use-case composition fixture (`internal/wire/container_test.go`), and the
  debug-diagnostic secret-sentinel regression.
- **T044**: `go test ./... -count=1` → all green; `go vet ./...` → clean;
  `git diff --check` → clean; obsolete-symbol audit clean.
- **T043 (line count)**: measured production files under `internal/infrastructure/telemetry`
  (excluding `*_test.go`): `config.go` 237, `processor.go` 318, `runtime.go` 475,
  `propagation.go` 89 → **total 1,119 physical lines**, above the 712-line SC-001
  target. `decorators.go` (609 lines) was removed. Further compression is required
  to meet the 712-line budget.

## Stage 5 — Collapse owned configuration (T027-T035)

- **T031**: `config.go` now owns exactly five gates — enabled, endpoint,
  deployment environment (APP_ENV fallback), baggage allowlist, and shutdown
  timeout. Shutdown overflow is rejected before millisecond-to-duration
  conversion. All tuning parsers, the `Compression` type, and the old tuning
  fields were deleted.
- **T032**: `runtime.go` delegates BSP/exporter tuning and sampling to the SDK.
  It bootstraps `OTEL_TRACES_SAMPLER=parentbased_traceidratio` and
  `OTEL_TRACES_SAMPLER_ARG=0.10` only when unset, applies the documented gzip and
  5-second exporter/BSP export-timeout defaults only when no effective SDK
  setting exists, installs a dropped SDK logger and the sanitized error handler
  before native parsing, and restores the meter provider, observability flag, and
  sampling bootstrap at shutdown.
- **T033**: `cmd/main.go` already routes through the single `loadTelemetry` path;
  no second parser was added.
- **T034**: `README.md` and `.env.example` document the five owned gates, the
  delegated native settings, defaults, precedence, sampling, and secret-safe
  diagnostics.
- **T027-T030/T035**: config tests rewritten around the five gates; runtime tests
  gained native sampling bootstrap/restore and malformed-native-tuning coverage;
  `cmd/main_test.go` covers field-only failures for the owned gates and
  credential-safe startup errors.
- Full suite: `go test ./... -count=1` → all green.
