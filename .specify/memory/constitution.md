<!--
Sync Impact Report
- Version change: unratified scaffold -> 1.0.0
- Modified principles:
  - Template Principle 1 -> I. Domain-Centered Clean Architecture
  - Template Principle 2 -> II. Domain-Driven Business Integrity
  - Template Principle 3 -> III. SOLID Contracts and Explicit Composition
  - Template Principle 4 -> IV. PostgreSQL Correctness and pgx/v5
  - Template Principle 5 -> V. Verification and Operational Visibility
- Added sections:
  - Technology and Data Constraints
  - Development Workflow and Quality Gates
- Removed sections: none
- Follow-up TODOs: none
-->
# Gin Product Service Constitution

## Core Principles

### I. Domain-Centered Clean Architecture
All new features MUST preserve the one-directional dependency flow:
`delivery/http -> app/<module> -> domain <- infrastructure`. HTTP handlers MUST limit themselves
to transport concerns: decoding, validation, authentication context, invoking a use case, and
encoding responses. Application use cases MUST orchestrate business behavior through domain
contracts. Domain code MUST NOT import Gin, pgx, PostgreSQL details, or infrastructure packages.
Infrastructure MUST implement domain-defined ports, and `internal/wire` MUST remain the composition
root. A dependency that points outward requires an approved constitution amendment before merge.
This boundary keeps business behavior independently testable and prevents framework or persistence
choices from controlling the domain.

### II. Domain-Driven Business Integrity
Modules MUST use the e-commerce ubiquitous language established by the domain: products, variants,
options, inventory items, locations, stock movements, and reservations. Business invariants MUST
be enforced in domain entities, value objects, or application use cases, never solely in handlers
or SQL. Each write use case MUST identify its consistency boundary and execute all changes within
that boundary atomically. Inventory-changing operations MUST prevent negative or lost quantities
under concurrent requests through database constraints, locking, or atomic statements. Cross-module
behavior MUST communicate through explicit domain contracts rather than shared mutable state. These
rules make product and inventory state trustworthy across every delivery mechanism.

### III. SOLID Contracts and Explicit Composition
Each package and type MUST have one clear responsibility. Use cases MUST depend on narrow domain
interfaces rather than concrete infrastructure types, and implementations MUST remain substitutable
without changing business behavior. Interfaces MUST be split when consumers require unrelated
methods; abstractions MUST be introduced only at a real dependency boundary or when they remove
demonstrated duplication. Constructors MUST expose required dependencies and MUST NOT hide them in
package globals or service locators. Dependency wiring MUST be explicit in `internal/wire`,
following the repository-to-use-case-to-handler flow used by the current modules. This makes
dependencies, ownership, and change impact visible during review.

### IV. PostgreSQL Correctness and pgx/v5
PostgreSQL is the authoritative persistence store. New database work MUST use
`github.com/jackc/pgx/v5`, including `pgxpool` where pooling is required; adding another SQL driver,
ORM, or query abstraction requires an approved architecture decision and constitution amendment.
SQL MUST be parameterized, context-aware, and explicit about transaction boundaries. Row mapping
MUST remain in infrastructure models and MUST convert to domain types before crossing the repository
boundary. Monetary and quantity values MUST use exact decimal types, never binary floating point.
Schema invariants MUST be backed by constraints and versioned migrations, and repository behavior
MUST account for PostgreSQL error and concurrency semantics. These constraints protect data fidelity
and keep persistence behavior predictable.

### V. Verification and Operational Visibility
Every behavior change MUST include tests at the lowest layer that can prove it. Domain invariants
and use cases MUST have deterministic unit tests; HTTP contracts MUST have handler or route tests;
SQL, transactions, mappings, and constraint behavior MUST have PostgreSQL integration tests when
changed.
Regression fixes MUST include a test that fails before the fix. Tests MUST use isolated data and
MUST NOT depend on execution order. New request paths and database operations MUST emit contextual
structured logs, preserve error causes for diagnosis, and add or update meaningful metrics without
exposing credentials or customer-sensitive data. Public API changes MUST update generated API
documentation. Verification and observability are required because correctness must be demonstrated
before release and diagnosable afterward.

## Technology and Data Constraints

- The service MUST remain a Go service using Gin at the HTTP delivery boundary and PostgreSQL for
  durable product and inventory data.
- Dependency versions MUST be managed in `go.mod`. Upgrades MUST pass the complete test suite and
  receive review for behavior, security, and compatibility changes.
- New features MUST follow the current repository layer layout and request flow. A proposed newer
  pattern MAY replace it only through a documented architecture decision and a constitution
  amendment that defines the migration path.
- Identifiers, statuses, and measurements MUST use domain-specific types or validated values. New
  entity identifiers MUST use UUIDv7 unless an existing schema contract requires another type.
- Secrets MUST enter through configuration and MUST NOT be committed, logged, or returned by APIs.
  All external input MUST be validated at the delivery boundary and rechecked wherever a domain
  invariant requires it.
- Database changes MUST include reversible or explicitly documented forward-only migrations. Code
  MUST remain compatible throughout the intended deployment sequence.
- API errors MUST use stable machine-readable codes and appropriate HTTP status values. Breaking API
  changes require an explicit versioning and migration plan.

## Development Workflow and Quality Gates

1. Each feature specification MUST define domain language, invariants, ownership boundaries, API
   behavior, persistence effects, concurrency expectations, and observable success criteria.
2. The implementation plan MUST map work to the established layers and MUST call out transactions,
   migrations, contract changes, and justified deviations before coding starts.
3. Implementation MUST proceed from domain contracts and behavior to application orchestration,
   infrastructure adapters, delivery integration, and composition-root wiring. Parallel work MAY
   vary this order only when contracts are agreed first.
4. Reviewers MUST verify dependency direction, aggregate integrity, SOLID boundaries, parameterized
   pgx/v5 data access, error handling, security, and operational signals.
5. `go test ./...` and all feature-relevant PostgreSQL integration tests MUST pass before merge.
   Formatting, static analysis, migration validation, and API documentation checks MUST also pass
   when affected.
6. Any intentional exception MUST be recorded with its scope, rationale, owner, expiry or removal
   condition, and follow-up work. Undocumented exceptions MUST block merge.

## Governance

This constitution is the highest-authority engineering standard for this repository. Specifications,
plans, tasks, implementation, and reviews MUST comply with it. When another project document
conflicts with this constitution, this constitution prevails until formally amended.

Amendments MUST be proposed as a reviewed change to this file. Each proposal MUST state the reason,
affected principles, compatibility impact, migration or remediation plan, and version bump. Approval
requires acceptance by the repository maintainers responsible for architecture and delivery. An
approved amendment takes effect when merged; affected in-flight work MUST be reconciled before its
next merge.

Versions follow semantic versioning: MAJOR for removal or incompatible redefinition of governance,
MINOR for a new principle or materially expanded requirement, and PATCH for non-semantic
clarifications. The Sync Impact Report MUST agree with the version and amendment date.

Every feature and pull-request review MUST include an explicit constitution compliance check.
Complexity, boundary exceptions, and new foundational dependencies MUST be justified in writing.
Maintainers MUST review this constitution at least annually and when the service changes framework,
database technology, architectural style, or domain ownership boundaries.

**Version**: 1.0.0 | **Ratified**: 2026-09-08 | **Last Amended**: 2026-09-08
