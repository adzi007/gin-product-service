# Research: Simplify OpenTelemetry Tracing

**Date**: 2026-09-20
**Scope**: Read-only repository and pinned dependency inspection; no implementation or runtime tests performed.

## 1. Native batching cannot be observed by a SpanProcessor wrapper alone

**Decision**: Reject the proposed wrapper-only design as infeasible on SDK 1.46.0. Propose a native SDK drop-counter bridge plus a small exporter outcome wrapper; record this as decision D1 rather than pretending the SDK exposes an admission callback.

**Evidence**: `SpanProcessor.OnEnd(ReadOnlySpan)` returns no value. The native BSP queue and dropped count are private. On queue full the BSP increments its private count and, when experimental observability is enabled, calls `ProcessedQueueFull`; it does not call the global error handler for a drop. Export errors occur on a worker or shutdown path, not as an OnEnd result.

Primary references: [pinned BSP source](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/sdk/trace/batch_span_processor.go), [processor interface](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/sdk/trace/span_processor.go), [SDK observation implementation](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/sdk/trace/internal/observ/batch_span_processor.go). These same files were inspected in the local module cache.

**Proposed integration**:

- Embed public metric no-op implementations; intercept only the SDK observation scope and `otel.sdk.processor.span.processed` counter.
- Count only additions tagged `error.type=queue_full` and `otel.component.type=batching_span_processor`; add the supplied count directly to existing `telemetry_spans_dropped_total{reason="queue_full"}`.
- Ignore successful processed-span additions and unrelated instruments; do not publish component IDs or SDK metric names.
- Set `OTEL_GO_X_OBSERVABILITY=true` and install the bridge before BSP construction at enabled singleton bootstrap. The flag has no equivalent public programmatic option. Do not import SDK internal packages.
- No OTel metrics SDK, reader, or exporter is added. The bridge only updates an already-existing Prometheus counter.
- Wrap the exporter for error classification; return a sanitized marker so the SDK error handler cannot double-count.
- Use bounded non-blocking warning handoff; never invoke a possibly slow sink synchronously in a drop callback.

**Rationale**: Counts real native rejection events without recreating capacity state. Native waiting queue capacity excludes the exporting batch, unlike the current gate, so saturation fixtures and documented capacity semantics must change.

**Alternatives considered**: offered-minus-exported estimates miscount queued/unsampled/failed work; queue-length checks race and cannot observe private state; reflection/unsafe/private imports are unsupported; parsing `total_dropped` debug logs couples to unstable text and reports only at exports; retaining a gate violates the requested retirement. A stable-hook-only policy leaves stage 3 blocked.

**Version/lifecycle limits**: Experimental names may change on SDK upgrades. Pin and regression-test the contract; install once before workers; isolate and restore global provider/logger/error-handler and environment in serial tests after shutdown. Do not add a fake SpanProcessor merely to match the requested type or line target.

## 2. Shutdown must reserve time within a single deadline

**Decision**: Use one total shutdown deadline and a shorter HTTP-drain deadline that reserves telemetry time. Flush uses the total context, not the drain context. Dependency closure cannot precede and block the flush.

**Evidence**: `cmd/server/gin_server.go` currently calls HTTP Shutdown, synchronous database Close, then telemetry Shutdown with the same context. The budget constant is duplicated there and in telemetry/config.go. `cmd/main.go` also defers an unconditional DB close.

**Rationale**: A fresh full timeout after HTTP drain could exceed the service budget; a child of an expired drain context would remain expired. Reserving a slice gives already-accepted spans a positive opportunity while retaining the earlier caller deadline. Concurrent bounded waiting for DB close addresses a second starvation source. Force-close active HTTP connections on drain timeout so their contexts cancel.

**Alternatives considered**: unbounded Background flush violates caller and total deadlines; HTTP then flush on identical deadline reproduces starvation; immediately shutting down telemetry concurrently with a normal drain loses spans from requests that could have completed inside the allotted drain period. The chosen reservation preserves that opportunity.

**Boundary**: No delivery guarantee for spans ending after telemetry intake closes, and no successful delivery promise during outages. One shutdown lifecycle may export multiple batches and use bounded transport retries.

## 3. Removing decorators changes the trace graph

**Decision**: D2 explicitly chooses between application-span removal and exact immediate-parent preservation. Neither behavior is mislabeled as the other.

**Evidence**: `internal/wire/container.go` wraps each use case in a telemetry decorator. Database and Redis work therefore run under `app.*` parents. `middleware.TracingConfig.EnrichContext` and `router.SetupRouter` already enrich the HTTP context with `logger.WithTraceContext`. The concrete child logger stored in context does not refresh automatically for nested spans.

**Proposed removal interpretation**: Follow the later direct-use-case/HTTP-only-refresh instruction, remove application spans, and document database/Redis reparenting to HTTP. Update spec FR-008/FR-009, RC-B, SC-002 and trace expectations only after that interpretation is settled.

**Compatible alternative**: Explicit spans at existing HTTP use-case invocation sites retain old scope/names/parents without decorators or a telemetry import in wire. Each such site must refresh its child logger and close the span on failure/panic. This is compatible with the original spec but exceeds HTTP-only enrichment.

**Test effects**: Server and HTTP integration tests construct decorators directly and must be migrated with deletion. Simulated DB spans alone do not establish real pgx equivalence; include real database-backed HTTP requests.

## 4. Native configuration has different semantics

**Decision**: D3 requires an explicit field ownership/default/compatibility table. Do not treat SDK delegation as identical validation.

| Setting | Current service | Pinned SDK when delegated |
| --- | --- | --- |
| Queue / batch | 2048 / 512; rejects invalid bounds and batch > queue | Same defaults; clamps oversize batch; malformed integers can fall back; negative sizes can panic |
| Schedule delay | 5000 ms, validated positive with ceiling | 5000 ms default; native parsing/bounds differ |
| BSP export timeout | 5000 ms; constrained by budget | 30000 ms default |
| Exporter timeout | 5000 ms; constrained by budget | 10000 ms default |
| Compression | gzip | none unless configured |
| Headers | Service parser, fails malformed entries | SDK parser, logs malformed entries and may ignore them; supports native generic/trace precedence |
| Root sampler | ParentBased ratio; default 0.10; argument alone works | Default ParentBased AlwaysSample; ratio argument requires selecting parentbased_traceidratio |
| Service identity | gin-product-service; custom name length rule | Resource env/default detection has different defaults and accepts additional attributes |

**Evidence**: Local `sdk@v1.46.0/trace/{batch_span_processor.go,sampler_env.go,provider.go}` and `otlptracehttp@v1.46.0/internal/{otlpconfig,envconfig}`. SDK logs can contain malformed header contents and raw bad duration values before exporter construction finishes.

Primary references: [sampler env parser](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/sdk/trace/sampler_env.go), [exporter env parser](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/exporters/otlp/otlptrace/otlptracehttp/internal/envconfig/envconfig.go), [exporter options](https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@v1.46.0).

**Rationale**: Only five owned startup gates is compatible with fewer rules, but not with preserving every old tuning rejection. Default options may preserve timeout/compression defaults if applied only when no effective env setting exists. Sampling needs either controlled bootstrap selection/defaults for native env parsing or an explicit exception retaining one small owned sampler parser.

**Rejected shortcuts**: Hardcoding ratio .10 loses configured roots; accepting native default samples all roots; delegating arbitrary sampler mode permits non-parent-based sampling; retaining validateEnabled duplicates the validation owner; catching arbitrary constructor panics cannot reliably identify which field was invalid.

**Required resolution**: Specify whether native fallback/panic-prone input behavior is allowed, or permit a minimal boundary guard/dependency fix. Do not delete old rejection tests and call that equivalent behavior. The draft preserves this gate rather than deciding new startup behavior without disclosure.

## 5. Debug level does not sanitize error text

**Decision**: Propose safe debug diagnostics derived from the original error via an allowlisted formatter; do not emit arbitrary Error() text. D4 records the requested preference.

**Rationale**: Transport errors may contain URLs and exporter responses may contain arbitrary body text. Header parsing diagnostics can contain credentials. Regex replacement of known secrets is insufficient for unknown customer data.

**Alternatives considered**: Raw Zap error at debug fails the spec and constitution; type/reason-only output is the simplest robust option. Richer redaction must whitelist safe structure and omit unknown messages. Install a safe SDK logger before native parsing and route its diagnostics separately from exporter-failure counters.

## 6. HTTP middleware and dependency selection

**Decision**: Retain the current custom HTTP middleware for this feature. No otelgin migration is hidden inside decorator removal.

**Rationale**: go.mod does not include otelgin. Its middleware creates a differently scoped server span, collects additional HTTP attributes/metrics, and records Gin error text. Replacing current behavior needs separate compatibility work outside the specified six changes.

Primary reference: [otelgin v0.71.0 source](https://github.com/open-telemetry/opentelemetry-go-contrib/blob/instrumentation/github.com/gin-gonic/gin/otelgin/v0.71.0/instrumentation/github.com/gin-gonic/gin/otelgin/gin.go).

**Other decisions**: Keep Go 1.25.0; use trace/noop provider; reject nil runtime at server construction; propagate constructor errors without changing handler/domain signatures. Redis/JWT config relocation and the unexported server return type remain follow-ups.

## Research Outcome

Mechanisms and incompatibilities are established from source. The proposed bridge, graph interpretation, configuration-contract changes, and diagnostic policy remain visible decision gates in plan.md. Phase 1 artifacts document reviewable designs; they do not certify that those choices have been accepted or that application tests pass.

## Recorded Decisions (maintainer-approved)

- **D1 = adopt the experimental bridge**: native drops are observed through the SDK's experimental processing counter and bridged to the existing Prometheus queue_full counter, plus a small exporter outcome wrapper. `TokenGate`, `DropProcessor`, and `CapacityExporter` are retired; Stages 3-6 and Polish are unblocked.
- **D2 = remove `app.*` spans**: DB/Redis reparent to HTTP; application logs carry the HTTP span ID.
- **D3 = five owned gates + SDK delegation**: parent-based ratio sampling with 0.10 default is bootstrapped at process bootstrap.
- **D4 = allowlisted/redacting debug formatter**: unknown raw error text is omitted; no unrestricted `zap.Error(rawErr)`.
