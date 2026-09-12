# Gin Product Service

A Go/Gin microservice for product & inventory management (Shopify/Odoo-inspired), part of a larger e-commerce system. It currently implements **category**, **product** (with options, variants, and media), and **infrastructure health-check** modules. See [PRD.md](PRD.md) for the full target design spec — treat it as a superset of what's currently implemented (e.g. `reservations` and `stock_moves` tables exist in the schema but are not yet queried by the service).

## Architecture

Clean Architecture with a strict, one-directional dependency flow:

```
delivery/http (handler, router) → app/<module> (use cases) → domain (interfaces + entities) ← infrastructure (repository, database, logger, metrics)
```

- **Handlers** parse/validate HTTP input and call use cases.
- **Use cases** contain business logic and depend only on `domain` interfaces.
- **Domain** defines entity structs and the repository/use-case contracts — no implementation logic.
- **Infrastructure** implements those contracts (Postgres repositories, DB pool, logger, metrics).
- **`internal/wire/container.go`** is the manual dependency-injection composition root: it wires `repository → use case → handler` per module and exposes them via a `Container` struct, consumed by `cmd/server/gin_server.go`.

### Folder structure

```
gin-product-service/
├── cmd/
│   ├── main.go                    # entrypoint: loads env/logger, opens DB pool, starts server
│   └── server/
│       ├── server.go              # AppServer interface (Start/Use/Close)
│       └── gin_server.go          # gin.Engine setup, builds wire.Container, registers /metrics, graceful shutdown
├── internal/
│   ├── domain/                    # entity structs + repository/use-case interfaces only (category.go, product.go, variant.go, inventory.go, infra.go, product_readmodel.go)
│   ├── app/
│   │   ├── category/              # category use cases: insert / query / update / delete (one file each)
│   │   ├── product/                # product use cases: insert / query / update / delete / option / variant / media
│   │   └── infrachecker/           # DB health-check use case
│   ├── delivery/http/
│   │   ├── router.go                # SetupRouter: registers all routes
│   │   ├── handler/                 # category_handler.go, product_handler.go (Gin request handlers + response DTOs)
│   │   ├── dto/                     # request DTOs (product_dto.go, variant_dto.go) with ToDomain() mappers
│   │   └── middleware/              # (not yet implemented)
│   ├── infrastructure/
│   │   ├── database/                 # Database interface wrapping *pgxpool.Pool (database.go, postgres.go)
│   │   ├── repository/                # Postgres implementations (categoryRepo.go, productRepo.go, healthRepo.go)
│   │   ├── repository/model/          # DB row structs with `db:"..."` tags + ToDomain() mappers
│   │   ├── logger/                    # zap wrapper, request-scoped via logger.L(ctx)
│   │   ├── metrics/                   # Prometheus counters/histograms
│   │   ├── redis/                     # (not yet implemented)
│   │   └── s3/                        # (not yet implemented)
│   ├── utils/                        # shared helpers (globals.go)
│   └── wire/container.go             # manual DI composition root
├── migrations/                      # incremental ALTER/INDEX patches (see Database Schema below)
├── docs/                            # generated Swagger output (swag init)
├── PRD.md                           # target design spec, incl. full DBML schema
└── .env.example                     # DATABASE_URL, APP_ENV
```

Naming conventions:
- One file per use case in `app/<module>/` (e.g. `insertUseCase.go`, `queryUseCase.go`).
- Use case constructors return the `domain` interface type, not the concrete struct (e.g. `NewCategoryQueryUseCase(...) domain.QueryCategoryUseCase`).
- Repository methods wrap every query with `metrics.ObserveDB(<module>, <operation>)(time.Now())` for Prometheus timing.

## Tech stack & libraries

- **Go** 1.25
- **Web framework**: [Gin](https://github.com/gin-gonic/gin)
- **Database driver**: [pgx/v5](https://github.com/jackc/pgx) (`pgxpool`), raw SQL via `pgx.CollectRows` + `pgx.RowToStructByName`
- **Decimal codec**: `jackc/pgx-shopspring-decimal` + [shopspring/decimal](https://github.com/shopspring/decimal) — used for all monetary/quantity fields (never `float64`)
- **IDs**: `google/uuid` — UUIDv7 for new entities (Category is the one exception, using `int`)
- **Validation**: `go-playground/validator/v10`
- **Config**: `joho/godotenv` (loads `.env`)
- **Logging**: `go.uber.org/zap`, wrapped for request-scoped context logging
- **Metrics**: `prometheus/client_golang`, scraped at `/metrics`
- **API docs**: `swaggo/swag`, `swaggo/gin-swagger`, `swaggo/files`
- **Testing**: standard library `testing` only — no testify/gomock; use cases are tested against small hand-rolled fake repositories implementing the domain interfaces

## Database schema

There is no full "create schema" migration in this repo — the authoritative schema is defined as DBML in [PRD.md](PRD.md) (section 9) and corroborated against the actual SQL in `internal/infrastructure/repository/`. The `migrations/` folder only contains two idempotent incremental patches (`0001_add_variant_position.sql`, `0002_add_inventory_levels_unique.sql`) meant to be run manually against Postgres; the base tables must already exist in the target database.

| Table | Key columns |
|---|---|
| `category` | `id` (int, pk), `name`, `slug`, `thumbnail`, `description`, `created_at`, `updated_at`, `deleted_at` (soft delete) |
| `products` | `id` (uuid, pk), `handle` (unique), `title`, `description`, `vendor`, `category_id` (fk → category), `status` (`draft`\|`active`\|`archived`), `created_at`, `updated_at` |
| `product_options` | `id` (uuid, pk), `product_id` (fk), `name`, `position`; unique (`product_id`, `name`) |
| `product_option_values` | `id` (uuid, pk), `option_id` (fk), `value`, `position` |
| `variants` | `id` (uuid, pk), `product_id` (fk), `sku` (unique), `barcode`, `title`, `price` (numeric), `weight` (numeric), `position`, `options` (jsonb), `is_deleted`, `created_at`, `updated_at`. Stock is **not** a stored column — it's computed at query time via a lateral join over `inventory_levels`. |
| `inventory_items` | `id` (uuid, pk), `variant_id` (unique fk), `description`, `track_inventory`, `created_at` |
| `locations` | `id` (uuid, pk), `name`, `type`, `address` (jsonb), `is_default` |
| `inventory_levels` | `id` (uuid, pk), `inventory_item_id` (fk), `location_id` (fk), `available_qty`, `reserved_qty`, `updated_at`; unique (`inventory_item_id`, `location_id`) |
| `stock_moves` | `id` (uuid, pk), `inventory_item_id` (fk), `from_location_id`, `to_location_id`, `move_type` (`IN`\|`OUT`\|`TRANSFER`\|`ADJUST`\|`RESERVE`\|`UNRESERVE`), `quantity`, `created_by`, `reason`, `created_at` |
| `reservations` | `id` (uuid, pk), `inventory_item_id` (fk), `location_id` (fk), `order_id`, `quantity`, `reserved_at`, `expires_at` — defined in the schema but not yet used by any query in this service |
| `product_media` | `id` (uuid, pk), `product_id` (fk), `type` (`image`\|`video`), `url`, `alt_text`, `position` |
| `variant_media` | `variant_id` (fk), `media_id` (fk → product_media), `position`; unique (`variant_id`, `media_id`) |

Relationships: `product_options`/`variants`/`product_media` → `products` → `category`; `product_option_values` → `product_options`; `variant_media` → `variants` + `product_media`; `inventory_items` → `variants`; `inventory_levels`/`stock_moves`/`reservations` → `inventory_items` + `locations`.

## API endpoints

Base path: `/api/v1`. Full interactive docs are available via Swagger once the app is running: `http://localhost:5000/swagger/index.html`.

### Infra

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness probe — always 200 |
| GET | `/readyz` | Readiness probe — checks DB connectivity, 503 if unreachable |
| GET | `/metrics` | Prometheus scrape endpoint |
| GET | `/swagger/*any` | Swagger UI |

### Categories (`/api/v1/categories`)

| Method | Path | Purpose |
|---|---|---|
| POST | `/` | Create a category |
| GET | `/` | List categories (filter by `name`, paginate with `page`/`per_page`, sort by `name`/`created_at`) |
| GET | `/dropdown` | Lightweight `id` + `name` list for dropdowns |
| GET | `/:id` | Get a category by id |
| PUT | `/:id` | Update a category |
| DELETE | `/:id` | Soft-delete a category |

### Products (`/api/v1/products`)

| Method | Path | Purpose |
|---|---|---|
| POST | `/` | Create a product, including options, variants, gallery media, and initial stock (single transaction) |
| GET | `/` | List products (filter by `search`, `category_id`, `status`; sort by `title`/`created_at`/`category_name`) |
| GET | `/:id` | Get a product by UUID or handle |
| PATCH | `/:id` | Update product header fields (title, description, vendor, handle, category, status) |
| DELETE | `/:id` | Archive (soft delete) a product |
| POST | `/:id/restore` | Restore an archived product |
| DELETE | `/:id/purge` | Hard-delete a product (409 if variants have stock-movement history) |
| POST | `/:id/options` | Add an option with initial values |
| PATCH | `/:id/options/reorder` | Reorder options |
| PATCH | `/:id/options/:option_id` | Rename an option |
| DELETE | `/:id/options/:option_id` | Delete an option |
| POST | `/:id/options/:option_id/values` | Add an option value |
| PATCH | `/:id/options/:option_id/values/:value_id` | Update an option value |
| DELETE | `/:id/options/:option_id/values/:value_id` | Delete an option value |
| POST | `/:id/variants` | Create a variant |
| POST | `/:id/variants/bulk` | Bulk-create variants |
| PATCH | `/:id/variants/bulk` | Bulk-update variants |
| POST | `/:id/variants/bulk-delete` | Bulk-delete variants |
| PATCH | `/:id/variants/reorder` | Reorder variants |
| POST | `/:id/media` | Attach gallery media (bulk) |
| PATCH | `/:id/media/reorder` | Reorder gallery media |
| PATCH | `/:id/media/:media_id` | Update media alt text |
| DELETE | `/:id/media/:media_id` | Remove media from the gallery (cascades to variant links) |

### Variants (`/api/v1/variants`)

| Method | Path | Purpose |
|---|---|---|
| PATCH | `/:id` | Update a variant (sku, barcode, title, price, weight, stock, media) |
| DELETE | `/:id` | Delete a variant (soft if it has history, hard otherwise) |
| POST | `/:id/restore` | Restore a soft-deleted variant |
| POST | `/:id/media` | Attach existing gallery media to a variant |
| PATCH | `/:id/media/reorder` | Reorder a variant's media |
| DELETE | `/:id/media/:media_id` | Detach media from a variant (media stays in the gallery) |

### Inventory (`/api/v1/inventory`)

| Method | Path | Purpose |
|---|---|---|
| POST | `/reservations` | Atomically reserve checkout stock for one order at the default location (idempotent by `orderId`) |

`POST /api/v1/inventory/reservations` requires an `expiresAt` value with every
request: the order service owns the hold deadline, so the inventory service
applies no duration policy and persists the supplied instant unchanged. The
request body is:

```json
{
  "orderId": "11111111-1111-4111-8111-111111111111",
  "expiresAt": "2030-01-02T03:04:05.123456+07:00",
  "items": [{ "id": "22222222-2222-4222-8222-222222222222", "qty": 2 }]
}
```

`expiresAt` is an RFC 3339 timestamp with an explicit UTC offset and no more
than six fractional-second digits; it is normalized to UTC and returned in
RFC3339Nano form. Missing/malformed/offset-less/over-precision values return
`400 ERR_INVALID_EXPIRY`; a well-formed value that is not strictly future at
reservation time returns `422 ERR_EXPIRED_EXPIRY`. Retrying the same
`orderId`, items, quantities, and represented expiry returns the original
holds (HTTP 200) without another stock transfer; changing any of them returns
`409 ERR_RESERVATION_CONFLICT`. The reservation rows themselves are the sole
durable idempotency record.

> Note: category endpoints return `{"message": "success", "data": ...}`, while product/variant endpoints return `{"status": "success"|"error", "data"|"message": ..., "code": ...}`. The response envelope is not yet unified between modules.

## Setup

1. Copy the env file and fill in your values:
   ```bash
   cp .env.example .env
   ```
   Required variables:
   - `DATABASE_URL` — Postgres/Neon connection string, e.g. `postgresql://user:pass@host/db?sslmode=require`
   - `APP_ENV` — `development` or `production` (controls zap logger config)
   - `REDIS_REST_URL`, `REDIS_REST_TOKEN` — Upstash Redis REST credentials required by
     `POST /api/v1/inventory/reservations` for cross-instance coordination. These are
     coordination-only: PostgreSQL remains the durable correctness authority. If they are
     missing or unreachable the endpoint fails closed (`503 ERR_COORDINATION_UNAVAILABLE`)
     without changing inventory. The token is never logged or returned.

2. Ensure the target Postgres database already has the base schema (see [Database schema](#database-schema) / [PRD.md](PRD.md)) — there is no in-repo migration tool that creates the base tables. Apply the incremental patches in `migrations/` manually if needed:
   ```bash
   psql "$DATABASE_URL" -f migrations/0001_add_variant_position.sql
   psql "$DATABASE_URL" -f migrations/0002_add_inventory_levels_unique.sql
   psql "$DATABASE_URL" -f migrations/0005_checkout_reservation_integrity.sql
   ```
   `migrations/0006_order_owned_reservation_expiry.sql` drops the now-obsolete
   `checkout_reservation_requests` table. It is a post-rollout, forward-only
   migration: deploy the current revision and drain every old instance **before**
   applying it, because older instances still query that table.

3. Install Go dependencies:
   ```bash
   go mod download
   ```

## Running the app

```bash
# Run with hot reload (uses air, config in .air.toml)
air

# Run directly
go run cmd/main.go

# Build
go build -o ./tmp/main.exe ./cmd/main.go
```

The server listens on `:5000`.

## Testing

```bash
# Run all tests
go test ./...

# Run a single test
go test ./internal/app/infrachecker/ -run TestNewInfraCheckerUseCase_CheckDatabase
```

Tests use the standard library `testing` package only (no testify/gomock). Use cases are tested against small hand-rolled fakes implementing the domain repository interfaces; `internal/domain` has entity/value-object tests (status transitions, validation, quantity math); `internal/delivery/http/router_routes_test.go` asserts routes are registered. PostgreSQL tracing integration tests are gated by `TEST_DATABASE_URL` and skip when it is unset.

## Tracing (OpenTelemetry)

End-to-end tracing is opt-in and disabled by default. It is implemented as
infrastructure plus delivery middleware: no domain entity, business rule, or
application use case imports OpenTelemetry.

### Coverage

| Traffic | Traced |
|---|---|
| Every registered business route under `/api/v1` | Yes — one server span per request |
| `GET /readyz` | Yes — it exercises PostgreSQL |
| `GET /healthz`, `GET /metrics`, `/swagger/*any`, unmatched routes | No — unchanged behavior, no exported span |

A sampled request produces one connected hierarchy:

```text
HTTP GET /api/v1/products/:id          (server)
`-- app.product.query.get_by_id        (internal)
    |-- pool.acquire                   (client, PostgreSQL)
    `-- SELECT                         (client, PostgreSQL)
```

Reservation requests additionally carry `redis.reservation.acquire` and
`redis.reservation.release` client spans around the transactional database work.
Span names use only the matched route template and a bounded
`app.<module>.<operation>` vocabulary; raw URL paths, query strings, UUIDs, and
handles are never used.

### Configuration

Every variable, its default, and its bounds are documented in
[`.env.example`](.env.example). Defaults are safe to leave in place: tracing stays
off until `OTEL_TRACING_ENABLED=true` and an endpoint is supplied.

| Variable | Default | Notes |
|---|---|---|
| `OTEL_TRACING_ENABLED` | `false` | Strict boolean. Disabled creates no exporter, worker, pgx tracer, or middleware. |
| `OTEL_SERVICE_NAME` | `gin-product-service` | Exported as `service.name`. |
| `OTEL_DEPLOYMENT_ENVIRONMENT` | none (`APP_ENV` fallback) | Required when enabled; exported as `deployment.environment.name`. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | none | Required when enabled; absolute `http`/`https` URL, no user info/query/fragment. |
| `OTEL_EXPORTER_OTLP_TRACES_HEADERS` | none | Optional OTLP headers; values are never logged or exported. |
| `OTEL_EXPORTER_OTLP_TRACES_COMPRESSION` | `gzip` | `gzip` or `none`. |
| `OTEL_EXPORTER_OTLP_TRACES_TIMEOUT` | `5000` ms | Positive, within the 15 s shutdown budget. |
| `OTEL_TRACES_SAMPLER_ARG` | `0.10` | Decimal in `[0,1]`; new root traces only. |
| `OTEL_BSP_MAX_QUEUE_SIZE` | `2048` | Positive, bounded. |
| `OTEL_BSP_MAX_EXPORT_BATCH_SIZE` | `512` | Positive, no larger than the queue size. |
| `OTEL_BSP_SCHEDULE_DELAY` | `5000` ms | Positive, bounded. |
| `OTEL_BSP_EXPORT_TIMEOUT` | `5000` ms | Positive, within the shutdown budget. |
| `OTEL_TRACES_SHUTDOWN_TIMEOUT` | `5000` ms | Positive, strictly below the 15 s shutdown budget. |
| `OTEL_BAGGAGE_ALLOWLIST` | empty | Comma-separated baggage keys; empty means default-deny. |

### Sampling and propagation

Sampling uses `ParentBased(TraceIDRatioBased(OTEL_TRACES_SAMPLER_ARG))`: a valid
upstream sampling decision is honored, and the local ratio applies only when this
service starts a new root trace. Missing, malformed, or unsupported trace
metadata starts a new trace and never rejects the request. W3C `traceparent` and
`tracestate` are always propagated; baggage is default-deny and only allowlisted
keys are forwarded. Baggage never becomes a span or log attribute.

### Failure behavior

- The exporter boundary fails open: a slow or unavailable destination never
  changes a response, transaction, or lease.
- Buffering is bounded. When full, newly completed spans are dropped rather than
  delaying a request, and reported through
  `telemetry_spans_dropped_total{reason="queue_full"}`.
- Export failures are counted separately on
  `telemetry_exporter_failures_total{reason=...}`, so queue pressure and
  destination failure stay distinguishable. Warnings are rate-limited and
  sanitized — endpoints, headers, credentials, SQL, Redis commands, and payloads
  are never emitted.
- Enabling tracing with structurally invalid configuration fails startup with a
  safe field-level error.
- Request-scoped logs carry lowercase `trace_id` and `span_id` whenever a valid
  span is active.

### What is intentionally not included

Jaeger, an OpenTelemetry Collector, trace storage, retention, and any trace UI are
out of scope. The service owns only the OTLP exporter boundary; point
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` at an environment-managed destination if you
have one.

### Verifying exporter delivery without a backend

The exporter contract test starts a local OTLP/HTTP capture receiver, points the
real exporter at it, and asserts `POST /v1/traces`, the OTLP protobuf content
type, a decodable non-empty payload, resource identity, successful flush, and safe
handling of non-success or unavailable destinations. No backend is installed.

```bash
go test ./internal/infrastructure/telemetry/... -run 'TestOTLPHTTPExporter' -count=1 -v
```

End-to-end behavior (trace hierarchy, failure/interruption outcomes, log
correlation, sensitive-data exclusion, queue pressure, and graceful shutdown) is
covered by:

```bash
go test ./internal/infrastructure/telemetry/... -count=1
go test ./internal/delivery/http/... -count=1
go test ./cmd/... -count=1
go test ./... -count=1
go test -race ./...
go vet ./...
```

With a disposable PostgreSQL database, real pgx span parenting is verified by:

```bash
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/infrastructure/database/... -count=1
```

## API documentation

Swagger docs are generated from annotations on the HTTP handlers. Regenerate after changing any handler's annotations:

```bash
swag init -g cmd/main.go -o docs --parseInternal
```

Docs are served at `http://localhost:5000/swagger/index.html` while the app is running.
