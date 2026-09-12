# Quickstart: Validate End-to-End OpenTelemetry Tracing

This guide is executable after implementation. It validates the operator contract without
changing product, inventory, or API behavior.

## Prerequisites

- Go 1.25 and project dependencies (`go mod download`).
- A disposable PostgreSQL database with the project schema and migrations applied.
- `.env` values for `DATABASE_URL`, `APP_ENV`, and `API_JWT_SECRET`.
- `REDIS_REST_URL` and `REDIS_REST_TOKEN` for the reservation trace scenario.

Never paste real OTLP, database, Redis, or JWT credentials into test output.

This feature does not install or configure Jaeger, an OpenTelemetry Collector, trace
storage, or a trace UI. Exporter correctness is verified by an automated local capture
receiver that exists only for the duration of the test.

## 1. Verify the OTLP/HTTP exporter without a backend

Run the focused exporter contract test added by this feature:

```bash
go test ./internal/infrastructure/telemetry/... \
  -run 'TestOTLPHTTPExporter' -count=1 -v
```

The test starts an `httptest.Server`, points the real OTLP/HTTP exporter at it, emits and
flushes sampled spans, and then shuts both down. It must prove:

1. the exporter sends `POST /v1/traces` with the OTLP protobuf content type;
2. the request body decodes as a non-empty OTLP trace export request;
3. `service.name` and `deployment.environment.name` have the configured values;
4. a successful receiver response completes flush and shutdown without an export error;
5. non-success responses and an unavailable receiver are reported safely and do not leak
   endpoint headers or credentials.

This is the required exporter smoke test. It crosses the real serialization and HTTP
boundary without retaining trace data or requiring a vendor UI.

## 2. Optional: run against an environment-managed OTLP destination

If an OTLP-compatible destination already exists in your environment, set safe local
values (shown in Bash syntax). This guide does not create that destination.

```bash
export OTEL_TRACING_ENABLED=true
export OTEL_SERVICE_NAME=gin-product-service
export OTEL_DEPLOYMENT_ENVIRONMENT=local
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://your-otel-endpoint.example/v1/traces
export OTEL_TRACES_SAMPLER_ARG=1
export OTEL_BAGGAGE_ALLOWLIST=
export OTEL_BSP_MAX_QUEUE_SIZE=2048
export OTEL_BSP_MAX_EXPORT_BATCH_SIZE=512
go run ./cmd/main.go
```

Expected: startup succeeds without printing the endpoint headers or credentials.

## 3. Inspect one product trace when a destination is available

In another terminal, call an existing product/category route that reaches PostgreSQL:

```bash
curl -i http://localhost:5000/api/v1/categories?page=1\&per_page=10
```

In the destination's trace inspection tool, select service `gin-product-service`. The
newest trace must contain:

1. `HTTP GET /api/v1/categories` as the server span;
2. a child application operation such as `app.category.query.find_all`;
3. correctly parented pgx acquire and `SELECT` spans;
4. the exact final HTTP status;
5. no raw path, query value, SQL text, SQL parameters, credentials, or payload data.

The request-scoped application logs must include the same `trace_id` and current
`span_id` shown by the trace.

## 4. Inspect the reservation trace

Use valid disposable order/variant data and the documented reservation request:

```bash
curl -i -X POST http://localhost:5000/api/v1/inventory/reservations \
  -H 'Content-Type: application/json' \
  -d '{"orderId":"11111111-1111-4111-8111-111111111111","expiresAt":"2030-01-02T03:04:05+07:00","items":[{"id":"22222222-2222-4222-8222-222222222222","qty":1}]}'
```

Expected trace hierarchy follows
[the telemetry contract](contracts/telemetry-contract.md): one server span, one reservation
application span, Redis acquire/release client spans, and PostgreSQL transaction/statement
spans. Redis scripts, keys, tokens, request body, order ID, and variant ID must be absent.

## 5. Verify incoming sampling and malformed context

Send a valid unsampled parent:

```bash
curl -i http://localhost:5000/api/v1/categories \
  -H 'traceparent: 00-11111111111111111111111111111111-2222222222222222-00'
```

Expected: the response is unchanged and no sampled trace is exported, even though the
local root ratio is `1`.

Then send malformed context:

```bash
curl -i http://localhost:5000/api/v1/categories \
  -H 'traceparent: malformed'
```

Expected: the request is not rejected; a new locally sampled root trace is created.

## 6. Verify 4xx, 5xx, cancellation, and panic policy

- Exercise a protected route without auth. Expected: exact 401 on a non-error server
  span and no application/database span that did not run.
- Exercise an established dependency failure. Expected: 5xx server span and failing child
  marked error, with a safe classified description.
- Run focused test fixtures for deadline cancellation, client disconnect, and recovered
  panic. Expected: all started spans end; abnormal interruptions and panic mark the server
  span error.

These cases are automated; do not enable an artificial panic endpoint in production.

## 7. Verify baggage default-deny and allowlist

With an empty allowlist, send:

```bash
curl -i http://localhost:5000/api/v1/categories \
  -H 'baggage: safe-test=allowed,customer-secret=must-not-propagate'
```

Expected: neither key/value appears in spans or logs, and no baggage is sent to a fake
downstream transport. Repeat in the propagation test suite with
`OTEL_BAGGAGE_ALLOWLIST=safe-test`; only `safe-test` may appear in the outgoing baggage
header and it still must not become a span/log attribute.

## 8. Verify disabled and invalid startup modes

Disabled:

```bash
OTEL_TRACING_ENABLED=false go test ./... -count=1
```

Expected: business tests pass, no exporter is created, and no trace request is attempted.

Invalid enabled configuration:

```bash
OTEL_TRACING_ENABLED=true \
OTEL_DEPLOYMENT_ENVIRONMENT=local \
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT='not-a-url' \
go run ./cmd/main.go
```

Expected: startup exits before listening with an actionable field-level error that does
not echo credentials or header values.

## 9. Verify destination outage, queue pressure, and shutdown

Point the exporter at an unused local port after passing structural validation and run the
automated outage test. Expected: existing request responses and database behavior remain
unchanged, warnings are safe/rate-limited, and failure counters increase.

Run the blocking-exporter saturation test. Expected:

- request goroutines complete rather than waiting for telemetry capacity;
- newly completed spans are dropped when capacity is exhausted;
- `telemetry_spans_dropped_total{reason="queue_full"}` increases exactly;
- memory and goroutine counts remain bounded.

Send `SIGTERM` during accepted work. Expected: HTTP intake stops, accepted requests drain,
the tracer receives the remaining shutdown deadline, queued spans get one bounded flush
attempt, and the process exits within 15 seconds even if the destination is unavailable.

## 10. Automated verification

```bash
go test ./internal/infrastructure/telemetry/... -run 'TestOTLPHTTPExporter' -count=1 -v
go test ./internal/infrastructure/telemetry/... -count=1
go test ./internal/delivery/http/middleware/... -count=1
go test ./internal/infrastructure/redis/... -count=1
go test ./... -count=1
go test -race ./...
go vet ./...
git diff --check
```

With a disposable PostgreSQL database:

```bash
export TEST_DATABASE_URL="$DATABASE_URL"
go test ./internal/infrastructure/database/... ./internal/infrastructure/repository/... -count=1
```

All tests must pass with tracing enabled and disabled. Sensitive-data tests must report
zero forbidden values, and representative route-family traces must satisfy SC-001 through
SC-009 in [spec.md](spec.md). Sections 1 and 10 are sufficient to verify exporter delivery
and trace structure without installing a tracing backend; sections 2 and 3 are optional
environment-level smoke tests.
