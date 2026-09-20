# Telemetry Refactor Contracts

**Status**: Approved contract; D1-D4 decisions were recorded (maintainer-approved) in [plan.md](../plan.md). Approved outcomes: D1 = adopt the experimental SDK observability bridge plus a small exporter outcome wrapper; D2 = remove `app.*` spans; D3 = five owned startup gates plus SDK delegation; D4 = allowlisted/redacting debug formatter. All six stages and the Polish phase are unblocked; the D1 bridge is the explicit, documented experimental coupling described below.

## External surfaces

No HTTP method, route, response body, domain interface, or handler signature changes. /metrics remains the existing Prometheus scrape endpoint. No new exporter/backend/metrics or logs pipeline.

HTTP span names remain `HTTP <METHOD> <matched route template>`. Registered /api/v1 paths and /readyz remain traced; /healthz, /metrics, Swagger, and unmatched routes remain excluded. Preserve existing safe HTTP outcome attributes, expected 4xx behavior, 5xx errors, and cancellation/deadline/panic reporting.

The current custom middleware remains; otelgin is not presently a repository dependency. Adopting it would require a separate privacy/attribute/scope compatibility decision.

## Prometheus and diagnostic contract

| Counter | Labels | Vocabulary |
| --- | --- | --- |
| telemetry_spans_dropped_total | reason | queue_full, other |
| telemetry_exporter_failures_total | reason | transport, timeout, server_error, serialization, unknown, other |

Counters count real outcomes exactly once. Export failures count batch-call failures, not spans or every transport retry. No exporter URL, header, error message, span ID, or dynamic component name appears in labels.

All warnings are sanitized; at most one per runtime-wide window (default one minute). Metrics remain accurate while warnings are suppressed. A slow log sink cannot block request goroutines.

Debug severity does not permit raw credentials or collector response bodies. D4 proposes allowlisted diagnostic fields extracted from the original error; unrecognized raw text is omitted. Test every sink with debug enabled and secret sentinels.

## Proposed native drop observation boundary (D1)

The following is an experimental SDK coupling, not a public service metric contract:

- Pinned SDK: 1.46.0.
- Flag: OTEL_GO_X_OBSERVABILITY=true, enabled before BSP construction.
- Scope: go.opentelemetry.io/otel/sdk/trace/internal/observ.
- Instrument: otel.sdk.processor.span.processed.
- Filter: error.type=queue_full and otel.component.type=batching_span_processor.
- Mapping: Add(n) increments the existing Prometheus queue_full counter by n.
- All other instruments/attributes are ignored and none are externally exposed.
- Public metric API/noop embeddings only; no import of SDK internal code, no metrics SDK/reader/exporter.
- Startup owns the global hook once; disabled mode leaves it untouched. Tests isolate process-global mutations and restore them after shutdown. Concurrent runtimes cannot silently replace each other's counters.
- Dependency upgrades must exercise a real native queue saturation test, not only manually call the bridge.

This bridge and an exporter observer replace the requested single SpanProcessor wrapper, and D1 is selected: the experimental bridge is the implemented native-drop observation boundary. A forwarding processor that cannot observe admission is not an acceptable substitute.

## Configuration ownership

| Field / group | Owner after stage 5 | Preserved contract or explicit delta |
| --- | --- | --- |
| OTEL_TRACING_ENABLED | Service | false by default; bad enablement flag fails safely |
| OTEL_EXPORTER_OTLP_TRACES_ENDPOINT | Service | required safe absolute URL when enabled; no network probe |
| OTEL_DEPLOYMENT_ENVIRONMENT / APP_ENV fallback | Service | nonempty environment when enabled |
| OTEL_BAGGAGE_ALLOWLIST | Service | default deny, existing validation |
| OTEL_TRACES_SHUTDOWN_TIMEOUT | Service | 5000 ms default; positive and below shared service budget |
| OTEL_BSP_* | SDK | native env parsing; fallback/clamping semantics differ from old custom rejection |
| Exporter headers, compression, timeout | SDK | native env parsing/precedence; retain old supported defaults only with absent-setting fallback |
| OTEL_SERVICE_NAME | SDK resource/default selection | preserve gin-product-service fallback; old custom length rule is no longer owned |
| OTEL_TRACES_SAMPLER_ARG | Decision D3 | preserve configured ratio, parent-based behavior, and .10 default; a literal five-field ownership rule requires explicit bootstrap sampler selection/defaulting |
| OTEL_TRACES_SAMPLER | Decision D3 | arbitrary modes must not silently override the parent-based contract |

Defaults to preserve: queue 2048, batch 512, schedule delay 5000 ms, batch export timeout 5000 ms, exporter timeout 5000 ms, gzip, root ratio .10. Native BSP/exporter defaults differ for the last timeout/compression/sampling choices.

Compatibility options must not override explicit operator values. Where the native exporter recognizes generic and trace-specific settings, resolve presence with its precedence and document that generic settings become effective. Do not parse the values twice.

Native invalid tuning input may be ignored, clamped, or cause construction failure instead of the previous safe field error. Negative queue/batch settings need a boundary guard or accepted contract change. Retaining every old rejection contradicts limiting owned validation to five fields; stage 5 remains gated on this resolution.

Sampling alternatives: (a) retain one tiny owned ratio parser and explicit ParentBased sampler, a sixth-field exception; or (b) bootstrap the SDK sampler selector and missing ratio default before SDK construction, allow SDK parsing, and accept/document its invalid-input semantics. Do not silently accept default sampling of all roots.

Retry timing also changes if the native exporter defaults replace the existing custom retry settings. Preserve the current retry policy where feasible, or enumerate that operator-visible delta before stage 5; exporter deadline and shutdown wait must remain bounded regardless.

Before SDK constructors parse environment, route their logger/error handler through the safe diagnostic policy. Parsing failures do not count as export failures, and no original input is printed.

## Startup and constructor contract

Enabled invalid owned fields fail before listener binding with field name and a constant reason. Valid configuration starts even if the destination is down.

All configuration entry points use one validation path; programmatic test structs cannot bypass it. Disabled construction does not read/delegate unused tuning and performs no telemetry I/O.

At stage 6, server construction changes internally to return an error for nil telemetry; NewServer propagates it to main. Explicitly disabled runtime remains valid. This internal Go constructor change is authorized housekeeping and does not alter domain/HTTP handler interfaces.

## Propagation and logs

`propagation.go` and `propagation_test.go` remain byte-for-byte unchanged, including default-deny extraction and reinjection, Trace Context handling, and baggage filtering. Their current regression tests run unchanged.

The HTTP EnrichContext hook runs after server span creation and before the handler. D2 removal makes application request logs carry the HTTP span ID; the exact-parent alternative additionally refreshes each retained child span's logger.

## Shutdown contract

One shared production constant: internal/lifecycle.ServiceShutdownBudget = 15 seconds.

Stop computes a common absolute deadline, reserves a telemetry slice, drains HTTP within the shorter deadline, and calls runtime.Shutdown using a context that is not derived from the expired drain context. A slow dependency close must not delay that call. Caller cancellation and earlier deadlines still win.

Actual receipt of already-queued spans by a local receiver is required in the slow-drain regression. A fresh context alone is not proof of delivery opportunity. Test total duration, repeated stop, unavailable destination, and a blocking DB close.

Native SDK Shutdown bounds waiting but may drain using internal background contexts. Do not claim that every export goroutine is canceled merely because Shutdown returned. Process exit remains bounded, and tests must release controlled fake exporters after checking the deadline.

## Trace comparison and decision D2

Original acceptance requires the same HTTP/database names and immediate parent relationships. Removing decorator spans changes those relationships.

- Removal proposal: record only the deliberate removal of app.* spans and reparenting of their DB/Redis children to HTTP; all remaining names, attributes, sampling, and context continuity remain tested.
- Exact-parent alternative: preserve the app.* operation names/scope/parents at explicit call sites and retain original exact-graph acceptance.

Generated IDs/timestamps may be normalized, graph edges may not. Update the selected acceptance policy explicitly; do not flatten captured graphs to hide a change.
