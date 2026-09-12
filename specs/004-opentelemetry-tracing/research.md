# Phase 0 Research: End-to-End OpenTelemetry Tracing

## Decision: Use the stable OpenTelemetry Go SDK with OTLP over HTTP

- **Decision**: Pin the OpenTelemetry Go API, SDK, resource/semantic-convention modules,
  and OTLP HTTP trace exporter to the same 1.46.0 release line. Export protobuf traces
  over OTLP/HTTP to an environment-supplied compatible endpoint. Do not provision
  Jaeger, a collector, trace storage, or a visualization UI in this feature.
- **Rationale**: Tracing is stable in the Go implementation, OTLP preserves the native
  OpenTelemetry data model, and HTTP/protobuf avoids adding a second gRPC transport stack
  solely for observability. The endpoint, headers, compression, and timeout can use the
  standard OTLP environment vocabulary.
- **Alternatives considered**: OTLP/gRPC (valid but adds transport dependencies without
  a service requirement); a vendor-specific exporter (rejected because it weakens
  portability); stdout export (development aid only, not an operational destination);
  a bundled Jaeger all-in-one service (deferred because backend setup is out of scope).

## Decision: Validate an explicit opt-in configuration before creating an exporter

- **Decision**: Add a typed configuration loader. `OTEL_TRACING_ENABLED=false` is the
  default and creates no exporter or background worker. When enabled, validate the
  service name, deployment environment, endpoint URL, headers, ratio, batch bounds,
  timeouts, compression, and baggage allowlist before constructing the runtime.
- **Rationale**: OpenTelemetry libraries commonly fall back on defaults for malformed
  environment values, while FR-014 requires an enabled but unusable configuration to
  fail startup. Parsing once also prevents secrets from appearing in errors.
- **Alternatives considered**: Relying entirely on SDK environment parsing (rejected
  because fallback behavior cannot enforce the startup gate); silently disabling after
  any initialization error (rejected by FR-014); enabling by default (rejected by FR-012
  and safe rollout needs).

## Decision: Parent-based ratio sampling at the trace root

- **Decision**: Construct `ParentBased(TraceIDRatioBased(rootRatio))`, with a validated
  root ratio in `[0,1]`. Acceptance tests set the ratio to `1`; production selects its
  own bounded volume.
- **Rationale**: Parent-based sampling preserves sampled and unsampled upstream choices,
  while ratio sampling applies only when this service creates a new root trace. This is
  exactly FR-003 and avoids fragmenting distributed traces.
- **Alternatives considered**: Always-on production sampling (unbounded volume);
  independent per-service sampling (breaks upstream decisions); tail sampling inside
  this service (belongs in a collector and is outside scope).

## Decision: Use W3C Trace Context and a filtering baggage propagator

- **Decision**: Always support W3C `traceparent`/`tracestate`. Do not install the standard
  baggage propagator directly. A custom wrapper extracts and injects only configured,
  syntactically valid allowlisted keys; the default empty allowlist propagates no baggage.
  Baggage is never copied into spans or logs.
- **Rationale**: Standard baggage instrumentation may forward every incoming key. That
  conflicts with the explicit allowlist and default-deny requirements and can disclose
  attacker-controlled or sensitive metadata to downstream services.
- **Alternatives considered**: Trace Context only forever (safe but cannot satisfy a
  future non-empty allowlist); the unfiltered standard composite propagator (unsafe);
  recording allowlisted baggage as span attributes (not required and increases exposure).

## Decision: Own inbound HTTP instrumentation in a small Gin middleware

- **Decision**: Implement custom middleware using the OpenTelemetry API. It filters to
  `/api/v1` and `/readyz`, extracts context, starts the server span before auth, installs
  the derived request context, adds `trace_id`/`span_id` to the request logger, and after
  the chain records the matched Gin route template, method, final status, cancellation,
  and duration. A tracing-aware recovery middleware records a constant safe panic event
  and emits the established 500 response.
- **Rationale**: The feature requires an exact route coverage boundary, no raw URL path,
  no header/body capture, expected 4xx status without trace error, and reliable recovered
  panic closure. Owning this narrow middleware makes those policies directly testable.
- **Alternatives considered**: `otelgin` middleware (well-supported and considered, but
  the service would still need custom filtering, redaction, logging, and recovery/status
  logic); wrapping the entire `http.Server` (does not know Gin route templates until
  routing completes); handler-by-handler server spans (too late for auth failures).

## Decision: Create application spans with composition-root decorators

- **Decision**: Add infrastructure decorators that implement the existing domain use-case
  interfaces, start one low-cardinality `app.<module>.<operation>` span per method, call
  the wrapped implementation with the derived context, refresh the request-scoped logger
  from that child context, and record safe error type/outcome. `internal/wire` wraps every
  handler-facing use case.
- **Rationale**: This gives every route a distinct application operation without importing
  OpenTelemetry into domain entities, domain contracts, or application business code.
  Existing logger calls receive child `trace_id`/`span_id` fields because the decorator
  rebinds the context logger after starting the application span.
- **Alternatives considered**: OTel calls inside use cases (outward technical dependency
  and broad constructor churn); a tracing port in `internal/domain` (pollutes business
  contracts with observability); only HTTP and DB spans (cannot attribute application time).

## Decision: Instrument pgx/v5 at pool configuration with safe defaults

- **Decision**: Use `github.com/exaring/otelpgx` 0.12.0 on
  `pgxpool.Config.ConnConfig.Tracer`, passing the explicit tracer provider. Retain its
  low-cardinality operation span names and disable SQL statement attributes and connection
  details. Never enable query-parameter capture.
- **Rationale**: The adapter supports Go 1.25 and pgx/v5 and covers acquire, connect,
  prepare, query, batch, and copy operations. `BEGIN`, `COMMIT`, and `ROLLBACK` travel
  through the pgx query tracer, so transaction work remains within the same request tree.
  Disabling SQL text/parameters provides a mechanical sensitive-data boundary.
- **Alternatives considered**: Hand-instrumenting every repository method (duplicates
  existing metrics and can miss individual statements/transaction lifecycle); changing
  to `database/sql` or an ORM (constitution violation); including sanitized SQL text
  (unnecessary for the required operation-level diagnosis).

## Decision: Manually instrument reservation coordination and inject propagation

- **Decision**: Inject an explicit tracer and filtering propagator into the Redis REST
  adapter. Create `redis.reservation.acquire` and `redis.reservation.release` client spans,
  inject propagation headers, and record only system, operation, outcome, and duration.
- **Rationale**: A generic HTTP transport records URL/network attributes that are not
  needed and cannot identify the business coordination operation safely. The adapter is
  the correct infrastructure boundary and already owns the opaque command and credentials.
- **Alternatives considered**: `otelhttp.Transport` (correct propagation but broader HTTP
  attributes than this contract permits); logging or attaching EVAL payloads/keys
  (explicitly prohibited); spans in the reservation use case (would not describe HTTP
  transport outcomes precisely).

## Decision: Use a bounded drop-new gate around the SDK batch processor

- **Decision**: Put a non-blocking capacity gate in front of the SDK batch processor and
  release capacity when the wrapped exporter receives each accepted span. When full, drop
  the newly ended span, increment `telemetry_spans_dropped_total{reason="queue_full"}`,
  and issue a rate-limited safe warning. Configure the underlying processor so accepted
  spans enqueue without a second silent-drop path; bound batch size, delay, and export
  timeout.
- **Rationale**: The SDK batch processor drops when its queue is full, but stable public
  APIs do not expose that count directly. An application-owned gate provides deterministic
  drop-new behavior and a Prometheus signal without blocking request processing or using
  experimental SDK self-observability.
- **Alternatives considered**: SDK `WithBlocking` alone (can delay requests); the default
  processor alone (drops are not available to the existing Prometheus surface); experimental
  `OTEL_GO_X_OBSERVABILITY` (unstable and would require a second metrics pipeline); a fully
  custom batching processor (larger and riskier than wrapping the maintained processor).

## Decision: Fail open after initialization and flush within one shutdown budget

- **Decision**: Export failures go to a sanitized, rate-limited OpenTelemetry error handler
  and Prometheus counter; they never reach handlers or repositories. On shutdown, stop
  intake, drain accepted HTTP requests, close downstream resources, then call telemetry
  shutdown with the time remaining in the service's single 15-second shutdown context.
- **Rationale**: Destination outages must not alter business outcomes, while accepted
  telemetry still deserves a bounded delivery attempt. A single budget prevents nested
  timeouts from extending shutdown indefinitely.
- **Alternatives considered**: Synchronous export (adds destination latency to requests);
  fatal exit after post-start export failure (violates fail-open behavior); an unbounded
  background flush (violates shutdown and resource constraints).

## Decision: Verify structure and real OTLP delivery without a tracing backend

- **Decision**: Use `sdk/trace/tracetest` for hierarchy/status/attribute tests, a blocking
  exporter for queue saturation, fake transports for propagation and destination failure,
  Gin `httptest` for middleware outcomes, and a `TEST_DATABASE_URL` integration test for
  pgx statement and transaction spans. Add an `httptest.Server` capture receiver that
  accepts OTLP/HTTP, decodes the protobuf request, and asserts the `/v1/traces` path,
  protocol content type, resource identity, non-empty span payload, flush completion,
  and safe non-success handling. Add explicit denylist assertions over every exported
  attribute/event.
- **Rationale**: Trace shape and redaction are data contracts that can be tested
  deterministically without a live backend. The capture receiver crosses the actual
  exporter serialization and HTTP boundary, while pgx integration proves real driver
  hooks. Full business tests run both enabled and disabled.
- **Alternatives considered**: UI-only inspection through Jaeger or another backend
  (requires extra infrastructure and is not repeatable); testing only an in-memory exporter
  (does not prove OTLP serialization or transport); snapshotting complete spans (brittle
  across semantic-convention releases); only mocking pgx (does not prove driver integration).

## Sources consulted

- [OpenTelemetry Go exporters](https://opentelemetry.io/docs/languages/go/exporters/)
- [OpenTelemetry Go sampling](https://opentelemetry.io/docs/languages/go/sampling/)
- [OpenTelemetry baggage security considerations](https://opentelemetry.io/docs/concepts/signals/baggage/)
- [OpenTelemetry Go API/SDK 1.46.0](https://pkg.go.dev/go.opentelemetry.io/otel)
- [OpenTelemetry OTLP/HTTP trace exporter 1.46.0](https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp)
- [OpenTelemetry Go batch span processor](https://pkg.go.dev/go.opentelemetry.io/otel/sdk/trace)
- [otelpgx 0.12.0](https://pkg.go.dev/github.com/exaring/otelpgx)
- Repository files: `go.mod`, `cmd/main.go`, `cmd/server/gin_server.go`,
  `internal/delivery/http/router.go`, `internal/infrastructure/database/postgres.go`,
  `internal/infrastructure/redis/reservation_locker.go`, `internal/infrastructure/logger/logger.go`,
  `internal/infrastructure/metrics/metrics.go`, and `internal/wire/container.go`.
