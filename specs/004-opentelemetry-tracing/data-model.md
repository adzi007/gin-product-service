# Data Model: End-to-End OpenTelemetry Tracing

This feature introduces no PostgreSQL tables, columns, migrations, or business entities.
The following are ephemeral operational records and configuration values only.

## TelemetryConfig

Parsed once at startup. Secret-bearing values are never formatted into errors or logs.

| Field | Environment source | Default | Validation / behavior |
|---|---|---|---|
| `Enabled` | `OTEL_TRACING_ENABLED` | `false` | Strict boolean. When false, no exporter, worker, pgx tracer, or tracing middleware is created. |
| `ServiceName` | `OTEL_SERVICE_NAME` | `gin-product-service` | Non-empty bounded identifier; exported as `service.name`. |
| `Environment` | `OTEL_DEPLOYMENT_ENVIRONMENT`, then `APP_ENV` | none | Required when enabled; exported as `deployment.environment.name`. |
| `Endpoint` | `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | none | Required when enabled; absolute `http`/`https` URL with host; no user info, query, or fragment. |
| `Headers` | `OTEL_EXPORTER_OTLP_TRACES_HEADERS` | none | Optional OTLP headers; parsed without echoing values. Never logged or exported. |
| `Compression` | `OTEL_EXPORTER_OTLP_TRACES_COMPRESSION` | `gzip` | `gzip` or `none`. |
| `ExporterTimeout` | `OTEL_EXPORTER_OTLP_TRACES_TIMEOUT` | `5000` ms | Positive and no longer than the shutdown budget. |
| `RootSampleRatio` | `OTEL_TRACES_SAMPLER_ARG` | `0.10` | Decimal in `[0,1]`; used only by `ParentBased(TraceIDRatioBased(...))`. |
| `QueueSize` | `OTEL_BSP_MAX_QUEUE_SIZE` | `2048` | Positive bounded integer. |
| `BatchSize` | `OTEL_BSP_MAX_EXPORT_BATCH_SIZE` | `512` | Positive and no larger than `QueueSize`. |
| `ScheduleDelay` | `OTEL_BSP_SCHEDULE_DELAY` | `5000` ms | Positive bounded duration. |
| `BatchExportTimeout` | `OTEL_BSP_EXPORT_TIMEOUT` | `5000` ms | Positive and no longer than the shutdown budget. |
| `ShutdownTimeout` | `OTEL_TRACES_SHUTDOWN_TIMEOUT` | `5000` ms | Positive and less than the service's 15-second shutdown limit. |
| `BaggageAllowlist` | `OTEL_BAGGAGE_ALLOWLIST` | empty | Comma-separated unique, valid baggage keys. Empty means no baggage extraction or injection. |

Unknown or malformed values do not leak their raw content in startup errors. Reasonable
upper bounds for queue size and timeouts are enforced in code so configuration cannot
defeat the resource and shutdown guarantees.

## TelemetryRuntime

| Field | Type | Rules |
|---|---|---|
| `Enabled` | boolean | Controls whether instrumentation is attached. Immutable after startup. |
| `TracerProvider` | OpenTelemetry tracer provider | Explicitly supplied to middleware, decorators, pgx, and Redis. No-op when disabled. |
| `Propagator` | text-map propagator | W3C Trace Context plus filtering baggage behavior. |
| `Shutdown` | bounded function | Idempotent; rejects new telemetry, drains accepted spans, and returns on completion or context deadline. |

The runtime is created in the startup/composition path and is not a business-domain
service. Global OpenTelemetry registration may be performed for library compatibility,
but all feature-owned components receive the runtime explicitly.

## Trace

| Field | Type | Rules |
|---|---|---|
| `TraceID` | 16-byte identifier | Continued from valid W3C context or generated for a new root. Used for log correlation. |
| `Sampled` | boolean | Inherited from a valid remote parent; otherwise chosen by the root ratio sampler. |
| `TraceState` | W3C trace state | Preserved from valid upstream context. |
| `Resource` | bounded attributes | Includes `service.name`, `service.version` when available, and `deployment.environment.name`. |
| `Segments` | ordered causal set | Server, application, database, and coordination spans related by parent IDs. |

### Trace state transitions

```text
incoming request
  -> context extracted or new root selected
  -> active while request/application/dependencies run
  -> server span ended with final response/interruption
  -> accepted by bounded queue OR dropped-new and counted
  -> exported, failed safely, or abandoned at shutdown deadline
```

No export state is persisted and no export failure changes the completed business result.

## Segment (OpenTelemetry span)

| Field | Type | Rules |
|---|---|---|
| `TraceID` / `SpanID` | identifiers | Valid correlation identifiers; span ID is unique within the trace. |
| `ParentSpanID` | identifier or empty | Server span uses upstream parent when valid; all internal/client spans use the active context. |
| `Kind` | server / internal / client | HTTP entry is server; application is internal; PostgreSQL and Redis are client. |
| `Name` | bounded string | Drawn only from the vocabulary in `contracts/telemetry-contract.md`. |
| `Start` / `End` | timestamp | Every started span ends on success, error, cancellation, timeout, or recovered panic. |
| `Attributes` | bounded key/value set | Semantic attributes plus safe service vocabulary; no payloads, IDs, secrets, SQL text, or raw paths. |
| `Status` | unset / error | Server 4xx remains unset; server 5xx/panic/abnormal interruption is error; failing child operation remains error. |
| `Events` | bounded safe events | Errors/panics use constant or classified descriptions; raw error data is excluded when it could contain secrets or customer data. |

## Segment relationships

```text
HTTP <METHOD> <route-template>                    (server)
`-- app.<module>.<operation>                     (internal)
    |-- SELECT / INSERT / UPDATE / ...            (client, PostgreSQL)
    |-- BEGIN / COMMIT / ROLLBACK                 (client, PostgreSQL)
    `-- redis.reservation.acquire|release         (client, Redis REST)
```

Authentication or validation rejection before a use case has only the server span.
Unstarted application/database/coordination work must not be synthesized.

## TelemetryDropRecord

An in-process Prometheus observation, not a stored entity.

| Field | Type | Rules |
|---|---|---|
| `Reason` | enum | Initially `queue_full`; bounded label vocabulary. |
| `Count` | monotonically increasing counter | Increment once per newly completed sampled span rejected by the capacity gate. |
| `WarningWindow` | duration | At most one safe warning per configured/internal rate-limit window; repeated drops remain visible in the counter. |

Exporter attempts that fail after dequeue use a separate bounded-reason counter and
rate-limited warning so queue pressure and destination failure are distinguishable.
