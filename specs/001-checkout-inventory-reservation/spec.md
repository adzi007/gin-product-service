# Feature Specification: Checkout Inventory Reservation

**Feature Branch**: Not created (no branch hook configured)
**Created**: 2026-09-09
**Status**: Draft
**Input**: User description: "Create a checkout reservation endpoint that accepts an order and variant quantities, reserves only available inventory, records reservation and stock history, updates inventory atomically, and prevents competing requests from over-reserving stock."

## Clarifications

### Session 2026-09-09

- Q: How long should a newly created checkout reservation stay active before it expires? → A: 60 minutes.
- Q: When a 60-minute reservation expires, should this feature automatically return its quantity to available stock? → A: Handle release in a later feature.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reserve Checkout Stock (Priority: P1)

An order service submits an order reference and one or more product-variant quantities during checkout so that available stock is held for that checkout before payment or order confirmation completes.

**Why this priority**: A checkout cannot reliably promise items to a buyer unless the requested stock is held before the next buyer can take it.

**Independent Test**: Submit a valid order containing multiple variants with sufficient stock and confirm that the service returns an active reservation for every requested item and that each item’s available quantity decreases by the requested amount.

**Acceptance Scenarios**:

1. **Given** every requested variant has sufficient available stock at the fulfillment location, **When** the order service creates a reservation, **Then** the service creates one active reservation per requested variant and returns the order reference, reservation identifiers, variant identifiers, quantities, statuses, and expiry times.
2. **Given** a request contains one or more valid variant identifiers and positive whole-number quantities, **When** the order service sends it to `POST /api/v1/inventory/reservations`, **Then** the service accepts the `orderId` and `items`, where each item’s `id` means the product variant identifier and `qty` means the requested quantity.

---

### User Story 2 - Reject an Unfulfillable Checkout Atomically (Priority: P1)

An order service receives a clear failure when any requested item cannot be reserved, without partially holding the other items in the order.

**Why this priority**: Partial holds make an order’s checkout state unreliable and can unnecessarily block inventory that the buyer cannot purchase.

**Independent Test**: Submit an order containing one sufficiently stocked variant and one variant whose requested quantity exceeds its available quantity; verify that no reservation, stock-history record, or quantity change is retained for either item.

**Acceptance Scenarios**:

1. **Given** at least one requested variant has less available stock than its requested quantity, **When** the reservation request is processed, **Then** the request fails with an insufficient-stock error that identifies the affected variant and no item from the request is reserved.
2. **Given** an item has an unknown, non-reservable, or untracked variant, or has no reservable inventory level, **When** it is submitted, **Then** the request fails without changing stock for any requested item.

---

### User Story 3 - Keep Competing Checkouts Consistent (Priority: P1)

Order services can submit overlapping checkout requests at the same time without allocating the same available units more than once, and can safely retry a timed-out request.

**Why this priority**: Overselling stock directly harms customers and requires costly manual remediation.

**Independent Test**: Issue concurrent reservation requests whose combined quantity is greater than the available quantity; verify that only requests covered by the available quantity succeed, that quantities never become negative, and that a retry of a successful request does not reserve stock again.

**Acceptance Scenarios**:

1. **Given** two checkout requests compete for the last units of a variant, **When** they are processed concurrently, **Then** the total quantity reserved by successful requests never exceeds the quantity that was available before either request began.
2. **Given** a caller retries a previously successful reservation with the same `orderId` and the same items, **When** the retry is received, **Then** the service returns the original reservation outcome without creating additional reservations or reducing availability again.
3. **Given** a caller submits an existing `orderId` with a different set of items or quantities, **When** it is received, **Then** the service rejects it as a conflicting request without changing inventory.

### Edge Cases

- The request has no items, a malformed or missing order/variant identifier, a non-integer quantity, a zero quantity, or a negative quantity.
- The same variant appears more than once in one request; the service rejects the request so the caller must provide one unambiguous requested quantity per variant.
- A configured default fulfillment location is unavailable, or an item has no inventory record at that location.
- A reservation request fails after some checks or records have been prepared; no reservation, stock-history, or inventory change from that request may remain.
- The shared cross-instance coordination facility is temporarily unavailable; the service fails safely without making an uncoordinated inventory promise.
- A previously active checkout hold reaches its expiry time before the order completes; a separately delivered reservation-lifecycle feature must make those units available again without releasing them twice.

## Scope

The caller-facing contract for this feature is `POST /api/v1/inventory/reservations` with this shape:

```json
{
  "orderId": "xxxx-xxxx-xxxx-xxx",
  "items": [
    { "id": "xxxx-xxxx-xxxx", "qty": 1 },
    { "id": "xxxx-xxxx-xxxx", "qty": 2 }
  ]
}
```

This feature creates checkout holds only. Completing an order, cancelling it, and automatically releasing expired holds are separate lifecycle capabilities. A reservation-lifecycle feature must be delivered before production use of expiring holds; this feature must create the information those capabilities need.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide the create-reservation service at `POST /api/v1/inventory/reservations` and accept an `orderId` plus a non-empty `items` collection; each item MUST contain `id` and `qty`.
- **FR-002**: The system MUST interpret each item `id` as a product variant identifier, resolve its inventory item internally, and never require callers to know internal inventory-item identifiers.
- **FR-003**: The system MUST reject a request unless the order and all variant identifiers are valid, every requested quantity is a positive whole number, and each variant appears at most once.
- **FR-004**: The system MUST select the configured default fulfillment location for this version of the request and reject the complete request when that location or an eligible inventory level cannot be resolved for any item.
- **FR-005**: Before confirming a hold, the system MUST verify that every requested quantity is no greater than that variant’s currently available quantity at the selected location.
- **FR-006**: When every item can be reserved, the system MUST create an active reservation for each item, decrease available quantity by the held amount, increase reserved quantity by the same amount, and record a corresponding reservation stock-history movement for each item.
- **FR-007**: The system MUST treat all effects of one reservation request as one consistency boundary: either every reservation, inventory adjustment, and stock-history entry is retained, or none is retained.
- **FR-008**: The system MUST maintain non-negative available and reserved quantities, and MUST never promise more of any variant than was available at the start of the successful request.
- **FR-009**: The system MUST coordinate reservation attempts across running service instances for every affected variant so simultaneous requests cannot lose quantity updates, over-reserve stock, or leave partial holds. If coordination cannot be established, the request MUST fail without changing inventory.
- **FR-010**: The system MUST make creation idempotent by `orderId`: an identical retry returns the existing reservation result, while a changed request for an existing order reference returns a conflict and makes no inventory change.
- **FR-011**: On success, the system MUST return a created result containing the order reference and, for every item, its reservation identifier, variant identifier, held quantity, active status, and expiration time.
- **FR-012**: On failure, the system MUST return a stable machine-readable error and a safe explanation. Insufficient stock and conflicting retries MUST be distinguishable from invalid input, missing inventory, and temporary coordination failures; responses for item-level failures MUST identify the affected variant without exposing configuration credentials or internal-only data.
- **FR-013**: The system MUST retain enough reservation state and stock-history information to reconcile, release, or consume every active checkout hold later.
- **FR-014**: The system MUST emit operational evidence for successful, rejected, retried, and coordination-failed reservation attempts, including the order reference and outcome but excluding secrets and customer-sensitive details.
- **FR-015**: The system MUST set every newly created checkout reservation to expire exactly 60 minutes after it is created.

### Key Entities

- **Checkout Reservation Request**: A request from an order service to hold stock for one order reference; it contains one or more unique product-variant quantities.
- **Reservation**: An active, time-bounded hold of a quantity of one inventory item for one order; it has an identifier, lifecycle status, creation time, expiration time, and later release/consumption information.
- **Inventory Level**: The quantity of one product variant at one fulfillment location, split into available and reserved quantities.
- **Reservation Stock Movement**: The auditable history record that explains a quantity changing from available to reserved for a checkout hold.
- **Order Reference**: The caller-supplied identifier that ties all reservations in one checkout together and makes retries safe.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In acceptance testing, 100% of valid multi-item checkout requests with sufficient stock create exactly one active hold per requested item and return a result containing every held item.
- **SC-002**: In acceptance testing, 100% of requests containing an unfulfillable item leave every requested item’s available and reserved quantities, reservations, and stock history unchanged.
- **SC-003**: In a test of at least 100 concurrent competing checkout requests for the same variant, the total successful held quantity never exceeds the initial available quantity and neither available nor reserved quantity becomes negative.
- **SC-004**: In acceptance testing, 100% of identical retries for a successful order reference return the original outcome without increasing the held quantity or creating duplicate reservations.
- **SC-006**: In acceptance testing, 100% of created holds have a matching auditable stock-history record and enough information for later release or consumption.
- **SC-007**: In acceptance testing, 100% of created reservations have an expiry time exactly 60 minutes after their creation time.

## Assumptions

- This is a service-to-service operation; caller authentication and authorization follow the existing internal-service policy.
- `orderId` and item `id` values are UUID-form identifiers; `id` is specifically a variant identifier, while the internal inventory item remains hidden from callers.
- The current checkout contract intentionally does not accept a location. This version reserves from the single location configured as the default fulfillment location; location-selection or split-fulfillment rules are outside this feature.
- Reservations expire 60 minutes after creation. The expiration time is assigned by the service and is not supplied by callers; automatic expiry/release execution is outside the create-endpoint scope.
- A separate reservation-lifecycle feature will complete, cancel, or expire active holds and return released quantities to availability. It is required before production use of this endpoint, but is outside this feature’s scope.
- The requested reservation stock-history record is part of this feature, superseding the older draft inventory note that treated such records as optional for reservation creation.
- A shared distributed locking capability is required for cross-instance coordination. The implementation plan will use the user-provided Redis service configuration (`REDIS_REST_URL` and `REDIS_REST_TOKEN`) without storing or exposing either secret in source code or responses.
- Redis coordination leases use a fixed five-second TTL (`PX 5000`); PostgreSQL locking and rechecks remain the correctness boundary if a lease expires. Performance acceptance testing is outside this feature's scope.

## Dependencies

- Product variants, inventory items, inventory levels, a default fulfillment location, reservations, and stock-history storage already exist and remain mutually consistent.
- Deployment configuration supplies a reachable shared coordination service and its credentials.
- A separately delivered reservation-lifecycle capability releases, consumes, or expires active holds so reserved stock does not remain unavailable indefinitely.
