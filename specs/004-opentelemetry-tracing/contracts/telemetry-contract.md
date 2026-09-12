# Telemetry Contract: End-to-End OpenTelemetry Tracing

This is an operator-facing contract. It changes no public HTTP request or response shape.

## Coverage

Detailed tracing is enabled for:

- every registered business route under `/api/v1`;
- `GET /readyz`, because it exercises PostgreSQL;
- the application operation invoked by those routes;
- pgx connection acquisition, statements, batches, and transaction lifecycle work;
- reservation coordination acquire/release calls and their outgoing trace propagation.

Detailed tracing is excluded by default for `/healthz`, `/metrics`, `/swagger`,
`/swagger/*any`, and unmatched routes. Excluded traffic preserves its current HTTP
behavior and does not create an exported server span.

## Causal shape

For a sampled successful product request:

```text
HTTP GET /api/v1/products/:id
`-- app.product.query.get_by_id
    |-- acquire
    `-- SELECT
```

For a sampled reservation request:

```text
HTTP POST /api/v1/inventory/reservations
`-- app.inventory.reservation.create
    |-- redis.reservation.acquire
    |-- BEGIN
    |-- SELECT / INSERT / UPDATE ...
    |-- COMMIT or ROLLBACK
    `-- redis.reservation.release
```

Actual statement count may vary with established business behavior. All emitted child
spans must share the request trace ID and have a valid causal parent.

## Naming vocabulary

| Segment | Name format | Examples |
|---|---|---|
| HTTP server | `HTTP <METHOD> <gin-route-template>` | `HTTP GET /api/v1/products/:id` |
| Application | `app.<module>.<operation>` | `app.review.create`, `app.inventory.reservation.create` |
| PostgreSQL | low-cardinality pgx operation | `SELECT`, `INSERT`, `BEGIN`, `COMMIT`, `ROLLBACK` |
| Coordination | fixed adapter operation | `redis.reservation.acquire`, `redis.reservation.release` |

Names and grouping attributes never contain UUIDs, category IDs, product handles,
review text, order IDs, URL query strings, raw URL paths, SQL, or Redis keys/commands.

## Required resource attributes

- `service.name`: configured service identifier, default `gin-product-service`;
- `deployment.environment.name`: configured deployment environment;
- `service.version`: build version when the build supplies one;
- standard SDK/language attributes added by the OpenTelemetry resource merger.

## Required span attributes

HTTP server spans contain only bounded/safe values needed for diagnosis:

- `http.request.method`;
- `http.route` using the Gin template;
- `http.response.status_code`;
- bounded server/protocol attributes when available.

PostgreSQL spans identify `postgresql` and the low-cardinality operation only. Redis
coordination spans identify `redis`, `acquire` or `release`, and a bounded outcome.

## Outcome rules

| Outcome | Server span status | Child span behavior |
|---|---|---|
| 2xx/3xx | unset | Successful children remain unset. |
| Expected 4xx | unset | A business operation may record its returned rejection; nonexistent downstream work is absent. |
| 5xx | error | The failing child is error when known; server status matches the response. |
| Cancelled/deadline exceeded/client disconnect | error | All started children end and record a bounded interruption type. |
| Recovered panic | error | A constant safe panic event is recorded; response remains the established 500 behavior. |
| Child failure recovered by established behavior | based on final HTTP result | The child remains error even if the server span succeeds. |

Error descriptions are classified and safe. Credentials, response bodies, raw transport
errors containing endpoints, and customer content are not attached.

## Propagation

- Valid W3C `traceparent` and `tracestate` are extracted and continued.
- Missing, malformed, or unsupported trace metadata starts a new trace and never rejects
  the HTTP request.
- An upstream sampled flag is honored. The local ratio applies only to new roots.
- Outgoing reservation coordination receives the active trace context.
- Baggage is default-deny. Only keys in `OTEL_BAGGAGE_ALLOWLIST` are extracted and
  re-injected; disallowed/malformed values are ignored. No baggage becomes a span or log
  field automatically.

## Log correlation

Every request-scoped log emitted while a valid span is active contains lowercase hex
`trace_id` and `span_id`. Existing log fields and preserved error causes remain intact.
Logs outside an active request are unchanged. Trace identifiers are correlation fields,
not authentication or authorization tokens.

## Configuration and failure behavior

The environment contract is defined in [data-model.md](../data-model.md). Enabled but
structurally invalid configuration fails startup before accepting requests, using a safe
field-level error that never includes header values or credentials.

This service owns the OTLP exporter boundary only. Jaeger, collector deployment, trace
storage, retention, and visualization are supplied externally and are not part of this
feature. Automated acceptance uses a temporary local OTLP/HTTP capture receiver to verify
real exporter serialization, delivery, receiver-response handling, and shutdown flush.

After successful initialization:

- exporter slowness or failure never changes an HTTP response, transaction, or lease;
- the queue is bounded and drops the newly completed span rather than blocking a request;
- queue drops increment `telemetry_spans_dropped_total{reason="queue_full"}`;
- exporter failures increment a bounded-reason counter;
- repeated warnings are rate-limited and sanitized;
- graceful shutdown uses the remaining service shutdown deadline to flush accepted data.

## Forbidden telemetry data

Automated tests scan exported resources, attributes, events, and correlated fields for:

- authorization/cookie values, JWTs, API keys, database/Redis/OTLP credentials;
- request or response bodies and review content;
- SQL text, SQL parameters, Redis scripts, command bodies, owner tokens, or lease keys;
- raw URL paths, query strings, product handles, UUIDs, order IDs, variant IDs, and user
  identity values;
- disallowed baggage keys or values.

The presence of any forbidden value is a contract failure.
