# Implementation Plan: Simplify OpenTelemetry Tracing

**Branch**: `development` | **Date**: 2026-09-20 | **Spec**: [spec.md](spec.md)

**Input**: `specs/005-simplify-otel-tracing/spec.md` and the user's six ordered implementation stages.

**Status**: Draft design with compatibility gates open. The SDK research is complete; the requested single SpanProcessor wrapper is not implementable against the pinned public API. Proposed alternatives and contract changes are explicit below. This document is not authorization to weaken regression assertions silently.

## Summary

Reduce the 1,781-line production telemetry package to at most 712 normally formatted physical lines. Preserve non-blocking requests, bounded shutdown, existing Prometheus names and labels, trace context, and secret-safe diagnostics. Keep `propagation.go` and `propagation_test.go` byte-for-byte unchanged.

Implement six independently green changes in exactly the requested order: shared budget; shutdown starvation fix; native non-blocking batching; direct use-case wiring; reduced configuration ownership; housekeeping. Each change includes its affected tests and fixture migrations before proceeding to the next stage. Do not commit an intermediate removal that fails to compile.

Three requested mechanisms conflict with the original spec or the pinned SDK: a processor wrapper cannot observe native drops/export errors; removing all application spans changes database parentage; delegating all tuning changes validation/default semantics. Raw debug error text also conflicts with the no-secret logging contract. See the decision register and [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25.0 as required by the current `go.mod`; this satisfies the requested Go 1.22+ family but does not establish Go 1.22 build compatibility. No downgrade is planned.

**Primary Dependencies**: Gin 1.12.0; pgx/v5 5.10.0 and pgxpool; otelpgx 0.12.0; OpenTelemetry API/SDK/OTLP-HTTP 1.46.0; prometheus/client_golang 1.24.1; Zap 1.28.0. The requested otelgin is not currently installed or used. Retain the existing HTTP tracing middleware for this refactor: otelgin's defaults change scope, attributes, errors, and coverage, and adding it alongside existing middleware would duplicate spans.

**Storage**: Existing PostgreSQL and Redis coordination; no schema, migration, transaction, SQL, domain, or persistence changes.

**Testing**: Go tests, local OTLP/HTTP capture receiver, fake exporter and warning sink, isolated Prometheus registry, controlled Redis transport, real PostgreSQL tracing integration, benchmarks, race checks, `go vet`.

**Target Platform**: Existing service deployment; Windows development workspace. Commands in [quickstart.md](quickstart.md) are PowerShell-compatible.

**Project Type**: HTTP service; operational tracing refactor.

**Performance Goals**: No request wait for export/queue/warning delivery; bounded SDK queue and warning work; no measurable disabled-mode latency increase; total shutdown at most 15 seconds or earlier caller deadline.

**Constraints**: No new public metric/label names, telemetry exporter, backend, or metrics/log export pipeline. No handler-signature/domain-interface/HTTP contract changes. Preserve baggage implementation unchanged. Native batching observability requires an explicit design deviation from the requested wrapper.

**Scale/Scope**: Five current production telemetry files; changes also touch lifecycle, server/main, HTTP integration tests, wire, logger diagnostics, and configuration documentation. No application business logic changes.

## Constitution Check

*Initial check*: Dependency direction, business integrity, pgx use, explicit composition, and absence of persistence/API changes pass. Raw export-error logging fails the confidentiality gate until the diagnostic policy is resolved.

*Post-design check*: The proposed redacted diagnostic design preserves confidentiality. No domain/application package imports telemetry. The drop-observation mechanism and trace/config compatibility decisions remain open; implementation must not treat this draft as an all-green gate.

| Principle / gate | Assessment |
| --- | --- |
| Domain-centered architecture | Pass: lifecycle holds only a duration constant; wire constructs direct use cases; instrumentation remains delivery/infrastructure-owned. |
| Domain integrity and PostgreSQL correctness | Pass: no business, SQL, transaction, migration, or concurrency change. |
| Explicit composition | Pass: providers, counters, and sinks are passed explicitly. If selected, the SDK metric hook is a narrowly documented singleton SDK-global boundary, not a domain service locator. |
| Verification and visibility | Conditional: exact drop accounting must use a real SDK observation hook, not inferred queue lengths or offered-minus-exported counts. |
| Confidentiality | Raw debug strings fail. Sanitized diagnostics at every level are the proposed resolution; severity does not make secrets safe. |
| Compatibility | Resolved: D1-D4 are recorded in the Decision Register below; implementation proceeds per the approved outcomes. |
| User staging order | Pass: six stages below; affected tests move with their stage. |

### Decision Register

| ID | Conflict and proposed resolution | Gate |
| --- | --- | --- |
| D1 | Native BSP `OnEnd` returns void, drops are private, and exporter failures occur asynchronously. Proposed: an SDK-observability-to-existing-Prometheus bridge plus a small exporter outcome wrapper, with no custom admission gate. This replaces the single SpanProcessor / under-50-line mechanism, not the counter contract. | **Resolved: adopt the experimental bridge** (see Recorded Decisions). Experimental coupling is documented in the contract. |
| D2 | Direct use cases plus HTTP-only enrichment remove `app.*` spans. Proposed interpretation of the later instruction: intentionally remove them, preserve HTTP/DB names and trace ancestry, and document DB/Redis reparenting to HTTP. Alternative: preserve exact parents with explicit call-site spans and child logger refresh. | User interpretation pending; FR-008, FR-009, RC-B, SC-002 need synchronized wording if removal is selected. |
| D3 | Five owned startup fields plus SDK tuning delegation cannot retain every old field-level rejection. Proposed: preserve service defaults with small default selection, delegate SDK tuning validity/fallback semantics, document differences explicitly. Sampling remains parent-based with a 0.10 default; it cannot be dropped accidentally. | Configuration compatibility delta requires explicit spec reconciliation before stage 5. |
| D4 | Raw export errors may contain URLs, headers, or server response text. Proposed: debug diagnostics derived from the original error only through an allowlisted/redacting formatter; unknown text is omitted. Never use unrestricted `zap.Error(rawErr)`. | Diagnostic preference pending; unredacted output is not the default design. |

The questions for D1, D2, and D4 were raised during planning. Their proposed resolutions are reviewable designs, not recorded user answers. No application or spec behavior was silently changed.

### Recorded Decisions (maintainer-approved, T002)

| ID | Approved outcome | Consequence |
| --- | --- | --- |
| D1 | **Adopt the experimental observability bridge** plus a small exporter outcome wrapper. | Native drops are observed via the SDK's experimental processing counter and bridged to the existing Prometheus queue_full counter; no custom admission gate remains. `TokenGate`, `DropProcessor`, and `CapacityExporter` are retired (RC-A satisfied). Stages 3-6 and the Polish phase are unblocked. |
| D2 | **Remove** the per-use-case `app.*` decorator spans. | DB/Redis spans reparent directly under HTTP; application logs carry the HTTP span ID. FR-008/FR-009/RC-B/SC-002 are interpreted as documented-removal, not exact-parent preservation. |
| D3 | **Five owned startup gates** plus native SDK tuning delegation. | Own enabled/endpoint/environment/baggage/shutdown-timeout only; bootstrap parent-based ratio sampling with the 0.10 default; document SDK delegation deltas. |
| D4 | **Allowlisted/redacting debug formatter** at the exporter boundary. | Derive debug output only from allowlisted safe error fields; omit unknown raw text; never emit unrestricted `zap.Error(rawErr)`. |

Implementable scope under these decisions: **all six stages plus the Polish phase**. Stage 3 proceeds with the D1-approved bridge; Stages 4-6 follow in the mandated order.

## Project Structure

### Documentation (this feature)

```text
specs/005-simplify-otel-tracing/
  spec.md
  plan.md
  research.md
  data-model.md
  quickstart.md
  contracts/telemetry-contract.md
  checklists/requirements.md
  tasks.md                         # future speckit-tasks output, not created here
```

### Source Code (repository root)

```text
cmd/main.go                        # constructor errors; single cleanup owner
cmd/server/gin_server.go           # sibling drain/flush deadlines; direct wiring
internal/lifecycle/budget.go       # new single shutdown-budget constant
internal/delivery/http/
  router.go                        # HTTP enrichment registration
  middleware/tracing.go            # existing server span and enrichment hook
  tracing_integration_test.go      # replace decorator fixtures
internal/infrastructure/
  telemetry/
    config.go                      # five owned gates; single validation owner
    runtime.go                     # SDK composition and bounded shutdown
    processor.go                   # replace gate hierarchy with outcome adaptation
    propagation.go                 # UNCHANGED
    propagation_test.go            # UNCHANGED
    decorators.go                  # DELETE in stage 4
    decorators_test.go             # DELETE in stage 4
  metrics/metrics.go               # existing counters, no schema change
  database/postgres.go             # existing otelpgx integration
  redis/reservation_locker.go      # noop-provider housekeeping only
  logger/logger.go                 # safe debug diagnostic integration if needed
internal/wire/container.go         # direct use cases; no telemetry import
internal/domain/                   # unchanged
```

**Structure Decision**: Keep the established layout. The sole new shared home is a tiny dependency-free lifecycle package. Do not relocate the retired hierarchy elsewhere to satisfy the size target.

## Phase 0: Research Results

Completed source inspection of the pinned SDK, exporter, server shutdown, logger, wire, and instrumentation tests. Findings, source links, rejected alternatives, and unresolved product-contract decisions are recorded in [research.md](research.md). The requested stable processor callback does not exist; this is a confirmed constraint, not a coding task to invent later.

## Phase 1: Design and Ordered Implementation Handoff

The following are planned implementation stages, not completed changes. Before stage 1, record a green baseline, propagation file hashes, source line count, and representative trace captures. A pre-existing test failure is recorded and resolved separately, not masked by this refactor.

### 1. Consolidate ServiceShutdownBudget

- Add `internal/lifecycle/budget.go` with the sole `ServiceShutdownBudget = 15 * time.Second`.
- Replace both existing definitions in server and telemetry with imports. Update config validation, the still-existing `validateEnabled`, and tests referencing old package constants.
- Do not alter timeout values, comparisons, sequencing, or add duplicate alias constants.
- Verify focused server/telemetry tests, compile consumers, and `go test ./...`. No behavior test is invented for a mechanical constant move.

### 2. Fix shutdown starvation

- At `Stop` entry compute one total deadline from the earlier of service budget and parent deadline. Preserve parent cancellation.
- Obtain the effective telemetry shutdown timeout from the runtime through a small read-only accessor; disabled telemetry needs no reservation. Remove no config fields yet.
- Reserve up to that timeout from the available total window. Derive an HTTP-drain context ending at total deadline minus the reservation. Derive flush from the still-live total context, never from the drain context.
- Stop intake and drain HTTP. On drain expiry, close active connections so outstanding work is canceled. Start telemetry shutdown immediately after drain ends, even if drain expired.
- Run dependency close alongside the bounded telemetry attempt and wait only until the common total deadline; a blocking `pgxpool.Close` must not precede or starve flush. Ensure there is at most one cleanup worker.
- Reconcile `cmd/main.go`'s unconditional deferred `db.Close` with server cleanup ownership, so it cannot block process exit again after `Stop`. Retain cleanup on startup/constructor failure.
- Add a regression test with completed queued spans, batch delay longer than shutdown, a slow handler, and a capture receiver. Show failure against the old sequence, then actual exported spans with the new sequence.
- Test stalled exporter, blocking DB close, early/canceled parent, disabled telemetry, and repeated stop. Preserve one shutdown lifecycle, not a promise of one HTTP export request.
- Verify all existing lifecycle/integration tests and the full suite before stage 3. This fixes the bug without requiring gate/decorator/config removals.

### 3. Replace custom capacity management with native batching

**Entry gate: D1 must be resolved. A SpanProcessor-only wrapper is not a viable implementation.**

- Delete `WithBlocking`, `TokenGate`, `DropProcessor`, and `CapacityExporter` together. Keep the current config fields and explicit BSP options temporarily; configuration delegation belongs to stage 5.
- Proposed D1 design: create the native BSP over a small `SpanExporter` wrapper that classifies/counts failed batch exports once and returns a sanitized marker error.
- Observe native drops through the SDK's experimental processing counter, bridged directly to the existing Prometheus counter. The bridge implements public metric API interfaces by embedding no-op implementations; it does not import SDK `internal` packages, add a metrics SDK, expose new series, or manage capacity.
- Install bridge and sanitized SDK diagnostics before constructing the BSP/exporter. Enable `OTEL_GO_X_OBSERVABILITY` only at singleton enabled-tracing bootstrap. This environment/global dependency must be documented and covered by serial, isolated tests. Unknown instruments are no-op.
- A single bounded, non-blocking warning handoff lets caller-side metric increments proceed even if logging stalls. One worker owns the shared one-minute warning limiter; shutdown does not wait indefinitely for logging.
- Preserve failure vocabulary and unknown-reason normalization. SDK config warnings must not increment export-failure counters. The global error handler must not recount marker errors already handled by the exporter wrapper.
- Port saturation, exact drop counts, nonblocking requests, bounded resources, failure classification, warning limiting, and idempotent shutdown tests. Delete token acquire/release/reset/in-flight accounting tests. Keep tests of public outcome behavior, not gate mechanics.
- Configure one held in-flight batch plus the full native waiting queue before inducing drops. Native queue capacity excludes the exporting batch; the old gate counted it, so do not port its saturation threshold assertion unchanged.
- Verify metrics scrape contracts, SDK bridge filtering under concurrent drops, errors during final flush, and no extra public metrics. Do not claim the whole adaptation fits under 50 lines; target under 50 for the exporter outcome wrapper and budget the bridge explicitly.
- Run focused tests and the full suite before stage 4.

### 4. Remove per-use-case decorators and decouple wire

**Entry gate: D2 must be resolved and trace acceptance language synchronized.**

- Delete `decorators.go` and `decorators_test.go`; remove every decorator constructor in `internal/wire/container.go`.
- Change `wire.NewContainer(db, locker, runtime)` to `wire.NewContainer(db, locker)`; update the server caller. Wire must import neither telemetry nor logger solely for tracing.
- Pass direct use cases to unchanged handler constructors. Keep domain and handler signatures unchanged.
- The HTTP middleware already has `EnrichContext` and the router already supplies `logger.WithTraceContext`. Retain this single hook after server span start and before downstream handling; remove the decorator hook instead of adding a second middleware span.
- Proposed removal variant: use HTTP span IDs in application request logs and make database/Redis spans immediate HTTP children. Keep their names, resource identity, status contracts, and continuous trace IDs. Record this deliberate before/after topology difference.
- Exact-parent alternative: retain `app.*` spans at individual HTTP use-case invocation sites, including readiness, with explicit child-context logger refresh and deferred completion. A delivery-owned helper may use the active parent span's provider; no telemetry import in wire and no global lookup in business code. This alternative does not meet HTTP-only enrichment.
- Replace decorator-dependent probes in both HTTP and server integration tests in this same stage. For deletion, move still-relevant span completion, failure sanitization, and log-correlation assertions into surviving HTTP/integration tests; do not lose them merely by deleting the old file.
- Verify wire compilation/import list, every route family, disabled behavior, HTTP/database/Redis graph captures, and request-log IDs. Run the full suite before stage 5.

### 5. Collapse owned configuration

**Entry gate: D3 must be reconciled; the current SDK cannot preserve every old rejection by delegation alone.**

- Keep owned parsing/validation for enabled, traces endpoint, deployment environment with APP_ENV fallback, baggage allowlist, and telemetry shutdown timeout. Preserve strict endpoint safety and field-only errors. Reject overflowing shutdown milliseconds before converting to duration.
- Leave `propagation.go` and its tests untouched. Keep the allowlist grammar helper needed by the parser.
- Delete tuning fields, tuning parsers, and `validateEnabled`. Use one constructor path that validates the five fields exactly once; prevent external callers bypassing validation by replacing exported mutable config literals with construction through that path. Migrate fixtures in the same change.
- Let native environment handling configure BSP sizes/delays/timeouts and exporter headers/compression/timeout. No second parser in server/main.
- Preserve supported defaults with small default selection only where no effective SDK setting exists: service name, gzip, 5-second exporter timeout, and 5-second BSP export timeout. Do not override explicit generic or trace-specific settings. Document the precedence expansion from native generic exporter settings.
- Preserve parent-based root sampling and 0.10 default. The SDK's sampler env function is private and requires `OTEL_TRACES_SAMPLER=parentbased_traceidratio` to interpret the existing ratio argument. The proposed strict-five-gate variant selects that sampler mode at process bootstrap and defaults the missing argument without numeric parsing; the SDK owns ratio parsing. Isolate environment changes before goroutines and restore them in tests. If mutation of process configuration is rejected, a small sixth sampling parser is the alternative and requires an explicit scope exception.
- Document native malformed-value fallback, clamping, and newly recognized settings. Native negative queue/batch values can panic: the plan must not advertise the original field-level fail-fast guarantee for those values without an additional validated guard or dependency fix. This is a remaining D3 contract choice, not a hidden `recover` workaround.
- Install sanitized SDK logging/error handling before SDK parsers run; native parsing diagnostics can contain raw header values. Parser diagnostics do not count as failed exports.
- Replace tests for retired service-owned tuning rules with selected native SDK behavior assertions; preserve five-field failure tests and disabled/secret-safety regressions. Update README/.env.example in the same stage.
- Run config/runtime/startup and full suites before stage 6.

### 6. Housekeeping

- Replace deprecated `trace.NewNoopTracerProvider()` with `trace/noop.NewTracerProvider()` in runtime and Redis (and any affected test fixtures).
- Remove the commented stdouttrace block; remove the unused stdout exporter dependency through `go mod tidy` if no live use remains. Review the dependency diff; no blanket upgrades.
- Make `NewServerWithOptions` return `(*ginServer, error)` and reject nil telemetry before route setup. Make `NewServer` propagate `(AppServer, error)`; handle it in main and migrate all test calls together. Keep the existing unexported concrete return type otherwise.
- Add the selected debug diagnostic formatter at the exporter boundary alongside sanitized accounting. Rate-limit repeated debug output through the bounded warning path. Unknown/untrusted error messages never pass through verbatim.
- Run secret-sentinel tests with debug logging enabled; include credentials in headers and fake receiver error bodies. Test nil constructor rejection and cleanup ownership.
- Finish full suite, race checks, vet, relevant PostgreSQL integrations, capture comparison, disabled benchmarks, package line count, and propagation hash equality. Record results rather than assuming the gates passed.

## Validation and Size Budget

[quickstart.md](quickstart.md) defines commands and required evidence. Every completed stage must pass its focused tests plus `go test ./...` before the next stage begins. PostgreSQL integration skips do not count as evidence; configure an isolated test database for final acceptance.

Planning allocation, not a measured result: configuration at most 190 lines; runtime at most 220; outcome reporting/bridge/warnings at most 160; unchanged propagation 89; total 659, leaving 53 lines of margin. If the design cannot fit, simplify honestly; do not relocate parsing/admission logic or compress formatting. The entire adaptation, not just the small exporter type, counts toward the 712-line limit.

Capture and retain both original and revised span graphs. The approved D2 policy determines whether comparison requires identical immediate parents or explicitly enumerated application-span removals. Never normalize away differences while claiming exact compatibility.

## Follow-ups (Out of Scope)

- Move Redis and JWT configuration reads out of `setupRoutes` in a separate feature.
- Resolve the exported constructor returning the unexported `ginServer` type separately.
- A future stable native SDK drop hook could retire an experimental bridge if D1 selects it; do not broaden this refactor into a vendor SDK or exporter change.

## Complexity Tracking

No constitution exception is silently approved. An unredacted debug-error implementation would violate confidentiality and remains gated. The proposed SDK observability bridge is extra integration complexity justified only by preserving exact existing drop counters without retaining a custom queue gate; its scope, global ownership, version pin, and removal condition are documented in the contract.
