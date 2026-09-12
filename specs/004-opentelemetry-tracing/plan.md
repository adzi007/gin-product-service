# Implementation Plan: End-to-End OpenTelemetry Tracing

**Branch**: `004-opentelemetry-tracing` | **Date**: 2026-09-12 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/004-opentelemetry-tracing/spec.md`

## Summary

Add opt-in, end-to-end OpenTelemetry tracing from Gin request entry through the
invoked application use case, pgx/v5 work, and Upstash Redis coordination. A new
infrastructure telemetry package will own validated configuration, OTLP/HTTP export,
parent-based sampling, bounded non-blocking buffering, propagation, and shutdown.
Custom delivery middleware will enforce safe route-based HTTP attributes and log
correlation, while explicitly wired application decorators and infrastructure adapters
will create child spans without putting observability concerns in domain entities or
business rules. This phase does not provision Jaeger, an OpenTelemetry Collector, or
another tracing backend; an automated local OTLP capture receiver verifies that the
exporter sends valid trace payloads and handles receiver outcomes correctly.

## Technical Context

**Language/Version**: Go 1.25.0

**Primary Dependencies**: Gin 1.12.0; pgx/v5 5.10.0 with pgxpool;
OpenTelemetry Go API/SDK and OTLP HTTP exporter 1.46.0; exaring/otelpgx 0.12.0;
zap 1.28.0; Prometheus client 1.24.1.

**Storage**: PostgreSQL remains the authoritative durable store; trace data is sent
to an environment-supplied OTLP-compatible endpoint and is never persisted by this
service. Trace backend deployment and storage are outside this feature.

**Testing**: Standard-library Go tests; OpenTelemetry `tracetest` in-memory exporter;
an `httptest.Server` OTLP/HTTP capture receiver with protobuf decoding; Gin `httptest`
middleware/route tests; fake/blocking exporters and transports;
PostgreSQL integration tests gated by `TEST_DATABASE_URL`; `go test ./...` and
`go vet ./...`.

**Target Platform**: Linux-hosted Go HTTP service on port 5000; local development on
Windows/Linux. Automated validation requires no installed tracing backend.

**Project Type**: HTTP microservice.

**Performance Goals**: Under representative production sampling, tracing adds no more
than 10% to p95 request duration; request goroutines never wait for a full telemetry
queue; telemetry memory and background work remain bounded.

**Constraints**: Disabled by default; valid upstream sampling is honored; only new root
traces use the configured ratio; no request/response bodies, auth values, SQL text or
parameters, Redis commands, raw resource identifiers, review content, credentials, or
raw URL paths are exported; expected 4xx responses do not mark the server span as an
error; exporter outages cannot change business behavior; invalid enabled configuration
fails startup; flush is bounded by the existing shutdown window; Jaeger, collector,
storage, and trace UI provisioning are excluded.

**Scale/Scope**: Every business route under `/api/v1`, `/readyz`, all application
use-case interfaces currently injected into handlers, every pgx operation on the shared
pool, and both reservation coordination operations. `/healthz`, `/metrics`, Swagger
assets, and unmatched routes are excluded by default. No API or database schema change.

## Constitution Check

### Pre-design gate

| Gate | Status | Plan response |
|---|---|---|
| I. Domain-Centered Clean Architecture | PASS | Domain types stay telemetry-free. Delivery owns server spans, infrastructure owns exporters/adapters, and composition-root decorators wrap existing domain use-case contracts. |
| II. Domain-Driven Business Integrity | PASS | Tracing observes existing product, review, inventory, reservation, transaction, and coordination behavior without changing invariants or consistency boundaries. |
| III. SOLID Contracts and Explicit Composition | PASS | A narrow telemetry runtime is constructed at startup and passed explicitly to middleware, decorators, pgx, and Redis. No service locator or hidden business dependency is introduced. |
| IV. PostgreSQL Correctness and pgx/v5 | PASS | Existing pgx/v5 remains in place. `otelpgx` is attached to `pgx.ConnConfig.Tracer`; SQL parameters and statement text are disabled, and no migration is required. |
| V. Verification and Operational Visibility | PASS | The design adds trace/log correlation, a Prometheus drop counter, safe exporter error reporting, unit/HTTP/integration tests, a real OTLP/HTTP capture-receiver test, an operator contract, and a backend-neutral end-to-end quickstart. |

No constitutional exception is required.

## Project Structure

### Documentation (this feature)

```text
specs/004-opentelemetry-tracing/
|-- plan.md
|-- research.md
|-- data-model.md
|-- quickstart.md
|-- contracts/
|   `-- telemetry-contract.md
|-- checklists/
|   `-- requirements.md
`-- spec.md
```

`tasks.md` is produced later by `$speckit-tasks`, not by this planning workflow.

### Source Code (repository root)

```text
cmd/
|-- main.go                              # load config; initialize logger, telemetry, DB
`-- server/gin_server.go                 # explicit runtime wiring and bounded shutdown

internal/
|-- domain/                              # unchanged telemetry-free entities/contracts
|-- app/                                 # unchanged business implementations
|-- delivery/http/
|   |-- router.go                        # route setup and coverage policy
|   `-- middleware/
|       |-- tracing.go                   # safe inbound server spans + log correlation
|       |-- tracing_test.go
|       `-- recovery.go                  # panic recovery tied to the active span
|-- infrastructure/
|   |-- telemetry/
|   |   |-- config.go                    # strict environment parsing and validation
|   |   |-- config_test.go
|   |   |-- runtime.go                   # provider/OTLP exporter/resource/lifecycle
|   |   |-- propagation.go              # W3C trace context + baggage allowlist
|   |   |-- propagation_test.go
|   |   |-- processor.go                 # bounded drop-new processor/reporting
|   |   |-- processor_test.go
|   |   |-- decorators.go                # use-case interface decorators
|   |   `-- decorators_test.go
|   |-- database/postgres.go             # optional safe otelpgx tracer attachment
|   |-- logger/logger.go                 # request trace_id/span_id fields
|   |-- metrics/metrics.go               # telemetry drop/failure counters
|   `-- redis/reservation_locker.go       # explicit safe client spans + propagation
`-- wire/container.go                    # wrap all handler-facing use cases explicitly

.env.example                             # documented safe tracing defaults
README.md                                # operator setup, coverage, and failure behavior
go.mod / go.sum                          # pinned telemetry dependencies
```

**Structure Decision**: Preserve the existing single-service Clean Architecture layout.
The telemetry implementation belongs under infrastructure, inbound lifecycle behavior
belongs in delivery middleware, and `internal/wire` remains the only place where
application implementations are wrapped with telemetry decorators before handlers
receive them.

## Implementation Sequence

1. Add strict telemetry configuration and a disabled no-op runtime; cover all invalid
   enabled configurations before wiring exporters.
2. Build the OTLP/HTTP runtime, parent-based sampler, allowlisted propagator, bounded
   processor, Prometheus drop/error reporting, and bounded shutdown tests. Add a local
   capture-receiver test that decodes the OTLP protobuf request and verifies `/v1/traces`,
   content type, resource identity, non-empty spans, flush success, and safe failure paths.
3. Add safe Gin tracing/recovery middleware and request-context log enrichment before
   authentication and handlers; retain Gin access logging and recovery semantics.
4. Add application-interface decorators and wire every handler-facing use case through
   them without changing application or domain implementations; refresh the contextual
   logger after each application span starts so its `span_id` is current.
5. Attach the explicit tracer provider to pgx via `otelpgx`, with query parameters,
   SQL text, and connection details excluded; verify transaction/query parenting.
6. Inject tracing into the reservation locker and propagate only trace context plus
   configured baggage keys; never instrument the raw Redis command payload.
7. Integrate lifecycle order: initialize telemetry before the DB, stop HTTP intake,
   drain requests, close dependencies, and flush telemetry within one bounded shutdown
   budget.
8. Update configuration/operator documentation and run the backend-neutral exporter,
   focused, integration, full-suite, race/static, and sensitive-data checks from
   [quickstart.md](quickstart.md). Do not add Jaeger or collector deployment files.

## Post-design Constitution Check

All gates remain **PASS**. The completed design introduces no domain dependency on
OpenTelemetry, no persistence or public API change, and no hidden business dependency.
Telemetry concerns are isolated at delivery, infrastructure, and composition boundaries;
the existing `context.Context` carries trace state through application contracts. The
data model, operator contract, and backend-neutral quickstart make exporter delivery,
safety, shutdown, and verification obligations explicit without adding a tracing backend.

## Complexity Tracking

No constitutional violations or justified exceptions.
