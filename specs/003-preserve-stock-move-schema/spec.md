# Feature Specification: Preserve Stock Move Schema

**Feature Branch**: Not created (no branch hook configured)

**Created**: 2026-09-11

**Status**: Draft

**Input**: User description: "delete all code and plan documentation related to inserting `reservation_id` in stock_moves table. Just stick to the current schema"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Keep the Stock Movement Schema Free of Reservation Identity (Priority: P1)

A developer or reviewer inspecting the stock-movement schema sees only the canonical columns and no reservation-identifier column, so the stock-movement audit trail remains a pure record of quantity and location changes.

**Why this priority**: A reservation identifier belongs on the reservation record; storing it on every stock movement duplicates identity and risks the two records drifting apart.

**Independent Test**: Inspect the schema source-of-truth definition for stock movements and confirm it contains exactly the canonical columns and no `reservation_id` column.

**Acceptance Scenarios**:

1. **Given** the canonical stock-movement schema, **When** its definition is inspected, **Then** it contains `id`, `inventory_item_id`, `from_location_id`, `to_location_id`, `move_type`, `quantity`, `created_by`, `reason`, and `created_at`, and no `reservation_id` column.
2. **Given** a database built from the project's migrations, **When** the stock-movement table is inspected, **Then** no `reservation_id` column exists on that table.

---

### User Story 2 - Record Reservation Movements With Existing Columns Only (Priority: P1)

When a checkout hold is created, the service records the resulting stock movement using only the existing stock-movement columns, without writing the reservation identifier into the movement.

**Why this priority**: The movement's job is to explain that quantity moved between available and reserved; the reservation record already carries reservation identity, so duplicating it on the movement is redundant.

**Independent Test**: Create a checkout hold and verify the recorded stock movement contains the inventory item, locations, movement type, and quantity — and no reservation identifier.

**Acceptance Scenarios**:

1. **Given** a valid checkout reservation, **When** it is created, **Then** exactly one stock movement is recorded per held item using the existing columns, and the movement contains no reservation identifier.
2. **Given** the reservation record for that hold, **When** it is inspected, **Then** it alone retains the reservation identifier and its association to the order.

---

### User Story 3 - Remove Code and Plan Documentation References (Priority: P1)

No source code or planning document instructs or performs the insertion of a reservation identifier into the stock-movement table.

**Why this priority**: Stale references mislead future maintainers into reintroducing the column or believing it exists.

**Independent Test**: Search the repository for instructions or code that insert a `reservation_id` into the stock-movement table and confirm none remain.

**Acceptance Scenarios**:

1. **Given** the full repository, **When** it is searched for any code path that writes a reservation identifier into a stock movement, **Then** none is found.
2. **Given** the planning documents (specifications, plans, tasks, quickstarts), **When** they are reviewed, **Then** none describes adding or inserting a `reservation_id` column on the stock-movement table.

---

### Edge Cases

- A document references `reservation_id` as a reservation identifier in a response payload or as the reservation record's own identity (not as a stock-movement column); such references are not in scope for removal.
- A previously applied migration may have added the column to a non-production database; the migration set must no longer include it, and the codebase must not depend on it.
- The reservation creation capability continues to work after the cleanup, producing the same stock-movement audit record minus any reservation identifier.

## Scope

This change is limited to the `stock_moves` table's `reservation_id` column. Reservation identity remains on the reservation record. The reservation API contract, its response fields, and the reservation lifecycle are unchanged.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The stock-movement table MUST contain only the canonical columns — identifier, inventory item, source location, destination location, movement type, quantity, creator, reason, and creation time — and MUST NOT contain a `reservation_id` column.
- **FR-002**: The schema source-of-truth artifacts and all database migrations MUST NOT add or require a `reservation_id` column on the stock-movement table.
- **FR-003**: No code path MUST insert, update, read, or otherwise reference a `reservation_id` column on the stock-movement table.
- **FR-004**: Reservation identity MUST remain stored on the reservation record only; a stock movement MUST NOT duplicate or carry the reservation identifier.
- **FR-005**: Creating a checkout hold MUST still record exactly one stock movement per held item using the existing stock-movement columns, preserving the quantity and location audit trail.
- **FR-006**: All planning documentation — specifications, implementation plans, task lists, and quickstarts — MUST be consistent with the current schema and MUST NOT instruct inserting a `reservation_id` into the stock-movement table.
- **FR-007**: Any code, migration, or documentation that references a `reservation_id` column on the stock-movement table MUST be removed or corrected so that no such reference remains.
- **FR-008**: The change MUST NOT alter the externally observable reservation contract or the existing inventory and reservation invariants (atomicity, non-negative quantities, idempotent retries, and no over-reservation).

### Key Entities

- **Stock Movement**: An auditable record of a quantity and/or location change for one inventory item; it carries the canonical columns only and no reservation identity.
- **Reservation**: A time-bounded hold of a quantity for one order; it is the sole holder of reservation identity and order association.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of schema source-of-truth artifacts and migrations define the stock-movement table without a `reservation_id` column.
- **SC-002**: Zero code or planning-document references to inserting or selecting a `reservation_id` column on the stock-movement table remain in the repository.
- **SC-003**: The complete automated test suite passes after the cleanup.
- **SC-004**: In acceptance testing, 100% of checkout reservations still record exactly one stock movement per held item using the existing columns, with no reservation identifier stored on the movement.

## Assumptions

- "reservation_id in stock_moves" refers specifically to a column on the stock-movement table, not the reservation record's own identifier nor the `reservationId` field in reservation responses.
- The current schema supplied with the request is authoritative and is the target the service must conform to.
- The reservation record continues to carry reservation identity; stock movements remain a pure audit trail and must not gain a reservation column.
- Documents that mention `reservation_id` only as a reservation response field or reservation identifier are out of scope and are left unchanged.

## Dependencies

- The existing checkout reservation capability and its RESERVE stock-movement recording behavior.
- The schema source-of-truth artifacts and the versioned migration set.
