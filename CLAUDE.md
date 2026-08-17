# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

A Go/Gin microservice for product & inventory management (Shopify/Odoo-inspired), part of a larger e-commerce system. Currently only the `category` module and infra health checks are implemented; `product`, `variant`, `inventory`, and `stock movement` modules described in [PRD.md](PRD.md) are not yet built. Treat PRD.md as the target design spec, not the current state — always verify against actual code in `internal/` before assuming an endpoint/entity exists.

## Commands

```bash
# Run with hot reload (uses air, config in .air.toml)
air

# Run directly
go run cmd/main.go

# Build
go build -o ./tmp/main.exe ./cmd/main.go

# Run all tests
go test ./...

# Run a single test
go test ./internal/app/infrachecker/ -run TestNewInfraCheckerUseCase_CheckDatabase

# Regenerate Swagger docs (from repo root)
swag init -g cmd/main.go -o docs --parseInternal
```

Server listens on `:5000`. Env vars are loaded via `.env` (see `.env.example`): `DATABASE_URL` (Postgres/Neon), `APP_ENV` (`development` or `production`, controls zap logger config).

## Architecture

Clean Architecture layering, strictly one-directional dependency flow:

```
delivery/http (handler, router) → app/<module> (use cases) → domain (interfaces + entities) ← infrastructure (repository, database, logger, metrics)
```

- **`internal/domain`**: Entity structs and interface definitions only (e.g. `category.go` defines both `Category` struct and `CategoryRepository`/`QueryCategoryUseCase` interfaces). No implementation logic lives here. This is the layer both `app` and `infrastructure` depend on — new modules should define their contracts here first.
- **`internal/app/<module>`**: Use case implementations (business logic), one file per operation (e.g. `insertUseCase.go`, `queryUseCase.go`, `updateUseCase.go` in `category/`). Constructors return the `domain` interface type, not the concrete struct (e.g. `NewCategoryQueryUseCase(...) domain.QueryCategoryUseCase`).
- **`internal/infrastructure/repository`**: Postgres implementations of domain repository interfaces via `pgx/v5`. Uses `pgx.CollectRows` + `pgx.RowToStructByName` to map query results directly to domain structs (relies on `db:"..."` struct tags). Every repo method wraps its query with `metrics.ObserveDB(<module>, <operation>)(time.Now())` for Prometheus timing.
- **`internal/infrastructure/database`**: `Database` interface wraps a `*pgxpool.Pool` (see `database.go`, `postgres.go`). Constructed once in `cmd/main.go` via `database.NewPool(ctx)` and passed down through `server.NewServer(db)`.
- **`internal/wire/container.go`**: Manual DI composition root. Wires repo → use case → handler per module and exposes them via a `Container` struct. New modules get wired here (repo → use case → handler), then the handler is passed into the router in `cmd/server/gin_server.go`.
- **`internal/delivery/http/router.go`**: Single `SetupRouter` method registering all routes under `/api/v1`, plus `/healthz`, `/readyz` (checks DB via `InfraCheckUseCase`), and `/swagger/*any`. New handler dependencies must be added to `SetupRouter`'s signature and threaded through `gin_server.go`'s call site.
- **`internal/infrastructure/logger`**: Wraps zap. Use `logger.L(ctx)` (not a global logger var) to get a request-scoped logger if one was attached via `logger.WithContext(ctx, fields...)`, else falls back to the base logger.
- **`internal/infrastructure/metrics`**: Prometheus metrics; scraped at `/metrics` via `promhttp.Handler()`, registered directly in `gin_server.go` rather than the router.
- **`cmd/server`**: `AppServer` interface (`server.go`) implemented by `ginServer` (`gin_server.go`), which owns the `gin.Engine`, builds the `wire.Container`, sets up graceful shutdown on SIGINT/SIGTERM with a 15s timeout, and closes the DB pool on exit.

### Conventions carried over from the PRD (apply to new modules even though not yet implemented)

- IDs for new domain entities beyond `Category` (which uses `int`) should be UUIDv7 (`github.com/google/uuid`), generated in the use case layer before calling the repository.
- Use `github.com/shopspring/decimal` for any monetary or quantity field — never `float64`.
- All app/repository methods take `ctx context.Context` as the first parameter.
- Multi-table writes (e.g. stock moves affecting `inventory_levels`) must be wrapped in a DB transaction at the repository layer.
