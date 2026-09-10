# Implementation Plan: Preserve Stock Move Schema

**Branch**: `003-preserve-stock-move-schema` | **Date**: 2026-09-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/003-preserve-stock-move-schema/spec.md`

## Summary

Ensure the `stock_moves` table never carries a `reservation_id` column, and remove
any code or plan documentation that inserts or references one. Reservation identity
stays solely on the `reservations` table; `stock_moves` remains a pure quantity and
location audit trail. The repository is already largely conformant, so this is a
verification-and-sweep change with no schema migration, no contract change, and no
behavior change.

## Technical Context

**Language/Version**: Go 1.25.0

**Primary Dependencies**: Gin 1.12.0; pgx/v5 5.10.0 with pgxpool; google/uuid (UUIDv7);
go-playground/validator; zap; Prometheus; swaggo.

**Storage**: PostgreSQL is the authoritative durable store.

**Testing**: Standard-library Go unit tests and handler/route contract tests;
PostgreSQL integration tests driven by `TEST_DATABASE_URL` (skipped when unset);
`go test ./...`, `go vet ./...`.

**Target Platform**: Linux-hosted Go HTTP service on port 5000.

**Project Type**: HTTP microservice.

**Performance Goals**: N/A — behavior-preserving cleanup; no throughput or latency
target changes.

**Constraints**: No schema migration; no change to the reservation API contract or to
inventory/reservation invariants (atomicity, non-negative quantities, idempotent
retries, no over-reservation); the stock-movement audit trail is preserved; reservation
identity is not duplicated onto stock movements.

**Scale/Scope**: A repository-wide sweep across schema artifacts, migrations, repository
code, and spec/plan/task documents. No new modules, endpoints, or dependencies.

## Constitution Check

### Pre-design gate

| Gate | Status | Plan response |
|---|---|---|
| I. Domain-Centered Clean Architecture | PASS | No new code or dependency-direction change; the sweep only removes or corrects stray `stock_moves.reservation_id` references. |
| II. Domain-Driven Business Integrity | PASS | Reservation identity remains on `reservations`; `stock_moves` remains the quantity/location audit trail; no invariant changes. |
| III. SOLID Contracts and Explicit Composition | PASS | No new contracts or composition changes. |
| IV. PostgreSQL Correctness and pgx/v5 | PASS | No new DDL; existing migrations already avoid altering `stock_moves`; any stray reference is removed rather than migrated. |
| V. Verification and Operational Visibility | PASS | Extend or retain a schema-preservation guard test and keep the full suite green. |

No constitutional exception is required.

## Project Structure

### Documentation (this feature)

```text
specs/003-preserve-stock-move-schema/
├── plan.md              # This file ($speckit-plan command output)
├── research.md          # Phase 0 output ($speckit-plan command)
├── data-model.md        # Phase 1 output ($speckit-plan command)
├── quickstart.md        # Phase 1 output ($speckit-plan command)
└── spec.md
```

`contracts/` is intentionally omitted: the feature changes no external interface
(FR-008). `tasks.md` is produced later by `$speckit-tasks`.

### Source Code (repository root)

No new source files. The sweep only touches existing files if a stray
`stock_moves.reservation_id` reference is found:

```text
db.dbml, db.dbdiagram                        # schema source of truth
migrations/*.sql                             # versioned DDL (no stock_moves.reservation_id)
internal/domain/inventory.go                 # StockMove entity
internal/infrastructure/repository/model/reservation_model.go   # StockMove row mapper
internal/infrastructure/repository/inventoryRepo.go             # RESERVE stock movement insert
internal/infrastructure/repository/productRepo.go               # ADJUST stock movement inserts
internal/infrastructure/repository/inventoryRepo_test.go        # schema-preservation guard
specs/001-checkout-inventory-reservation/    # plan/spec/task documentation
specs/002-order-owned-expiry/                # plan/spec/task documentation
specs/inventory-reservation.md               # legacy draft (only if it references stock_moves.reservation_id)
```

**Structure Decision**: Keep the established inventory module and clean-architecture
layout unchanged. The change is a targeted deletion/correction pass across existing
files, not a new package or layer.

## Post-design Constitution Check

All gates remain **PASS**. The design introduces no schema change, no new dependency,
and no contract change; it only removes or corrects stray `stock_moves.reservation_id`
references while preserving the existing reservation audit trail and inventory
invariants. The schema-preservation guard test (and the full suite) provide the
required verification.

## Complexity Tracking

No constitutional violations or justified exceptions.
