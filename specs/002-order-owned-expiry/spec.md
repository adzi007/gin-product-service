# Feature Specification: Order-Owned Reservation Expiry

**Feature Branch**: Not created (no branch hook configured)

**Created**: 2026-09-10

**Status**: Draft

**Input**: User description: "Change the reservation expiration time so the order service supplies it in the request body; expiration is not this service's business. Remove the separate checkout-reservation-request record decision and store the order identifier with reservations."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Honor an Order-Selected Expiry (Priority: P1)

An order service creates a checkout hold with the time at which that order's hold must end, so its own checkout and payment rules determine the reservation lifetime.

**Why this priority**: The order service owns the buyer-facing checkout deadline and must not have that deadline silently replaced by an inventory-service policy.

**Independent Test**: Submit a valid checkout reservation with a future expiry time and verify every returned hold has exactly that time; submit an expired or invalid time and verify no hold or inventory change is created.

**Acceptance Scenarios**:

1. **Given** an order request has reservable items and one valid future expiry time, **When** the order service creates a reservation, **Then** every hold for that order is active with the supplied expiry time.
2. **Given** an order request has an expiry time that is missing, malformed, or not later than the time the hold is created, **When** the order service creates a reservation, **Then** the request is rejected and none of its items are held.
3. **Given** an order request has a valid future expiry time, **When** the reservation is returned, **Then** the returned expiry time exactly represents the order service's supplied time.

---

### User Story 2 - Retry an Order Without Changing Its Hold (Priority: P1)

An order service can safely retry a reservation request after a timeout without creating duplicate holds or allowing a retry to change the original checkout deadline.

**Why this priority**: A communication failure must not cause stock to be held twice or let a later retry silently extend or shorten an order's hold.

**Independent Test**: Create a reservation, repeat the same order identifier, items, quantities, and expiry time, and verify the original result is returned without additional stock movement; retry with a changed expiry time and verify a conflict without any inventory change.

**Acceptance Scenarios**:

1. **Given** an order already has active holds created from the same order identifier, items, quantities, and expiry time, **When** the same request is retried, **Then** the service returns the original holds without creating more holds or changing availability.
2. **Given** an order identifier already has holds, **When** a retry changes any item, quantity, or expiry time, **Then** the service rejects the retry as conflicting and leaves the original holds and inventory unchanged.

---

### User Story 3 - Keep Order Association With Each Hold (Priority: P2)

An operator or follow-on reservation lifecycle capability can find all holds for an order from the hold records themselves, without a separate order-level checkout-request record.

**Why this priority**: The reservation is the durable business record of stock held for an order; duplicating an order-level request record adds data that is not needed for the reservation lifecycle.

**Independent Test**: Create a multi-item order hold and verify every hold retains the order identifier and that the order's existing holds can be identified from those hold records alone.

**Acceptance Scenarios**:

1. **Given** a successful multi-item order reservation, **When** its holds are examined, **Then** each hold retains the originating order identifier and the same supplied expiry time.
2. **Given** an order has already been reserved, **When** the system determines whether a request is a retry or a conflict, **Then** it bases that decision on the order's reservation records and does not require a separate checkout-request record.

### Edge Cases

- The supplied expiry time becomes current or past while the request is being processed; the complete request fails without holding stock.
- The supplied expiry time includes an offset rather than UTC; the represented instant is preserved consistently in the result and in every hold for the order.
- A retry has the same represented expiry instant in a different valid timestamp representation; it is treated as the same expiry time.
- An existing order has holds created under the previous service-assigned-expiry rule; the feature preserves those historical holds and does not alter their recorded expiry times merely because the ownership rule changes.
- A request fails after some validation or preparation; no hold, order association, or inventory adjustment from that request remains.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST require the order service to supply one `expiresAt` value with every create-reservation request, in addition to the existing order identifier and requested items.
- **FR-002**: The system MUST accept an expiry value only when it unambiguously identifies a time strictly later than the time the reservation is created.
- **FR-003**: For a successful request, the system MUST assign the caller-supplied expiry time, without calculating, replacing, extending, or shortening it, to every reservation created for that order.
- **FR-004**: The system MUST return the stored expiry time for every reservation item in a successful creation or identical retry response.
- **FR-005**: The system MUST continue to make reservation creation idempotent by order identifier. An identical retry is one with the same order identifier, item set, quantities, and represented expiry time; it MUST return the original reservation result without changing inventory.
- **FR-006**: The system MUST reject as conflicting a request that reuses an order identifier but changes its item set, quantities, or represented expiry time, without changing the original holds or inventory.
- **FR-007**: The system MUST retain the order identifier directly with every reservation created for that order, including every item in a multi-item reservation.
- **FR-008**: The system MUST use reservation records as the sole durable source for an order's reservation association and retry outcome; it MUST not create or retain a separate checkout-reservation-request record.
- **FR-009**: The system MUST preserve the existing all-or-nothing reservation consistency boundary: if expiry validation, order-identity handling, or any item reservation fails, no reservation or inventory adjustment from the request may remain.
- **FR-010**: The system MUST preserve historical reservations and their recorded expiry times during the change; the new caller-owned expiry rule applies only to newly created reservations.
- **FR-011**: The system MUST provide stable machine-readable errors that distinguish an invalid expiry time, an expired expiry time, and a conflicting retry from insufficient stock and other invalid reservation input.

### Key Entities

- **Order Reservation Request**: A request from an order service to hold one or more variant quantities for an order until the order service's supplied expiry time.
- **Reservation**: A hold for one inventory item that retains its originating order identifier, quantity, active status, reservation time, and caller-owned expiry time.
- **Order Reservation Set**: All reservations that share one order identifier; the set is used to return an identical retry or identify a conflicting reuse of that order identifier.
- **Expiry Time**: The unambiguous future instant supplied by the order service that defines when every hold in one order reservation set ends.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In acceptance testing, 100% of valid multi-item requests create holds whose returned and retained expiry times exactly match the expiry time supplied by the order service.
- **SC-002**: In acceptance testing, 100% of requests with a missing, invalid, current, or past expiry time leave reservations and inventory quantities unchanged.
- **SC-003**: In a test of at least 100 repeated requests, 100% of identical retries return the original holds without creating duplicate holds or changing inventory, and 100% of retries with a changed expiry time are rejected without inventory change.
- **SC-004**: In acceptance testing, 100% of newly created multi-item order reservations retain the originating order identifier on every hold and can be evaluated for retry or conflict from those holds without a separate checkout-request record.
- **SC-005**: In stakeholder acceptance testing, the order service can select the checkout-hold deadline for every valid new reservation without an inventory-service duration policy overriding it.

## Assumptions

- The existing create-reservation contract's camel-case naming is retained, so the new request field is named `expiresAt`; it represents the same business value retained as a reservation's expiry time.
- One order reservation request has one expiry time that applies to all items in that request.
- The order service is responsible for choosing a valid checkout deadline. The inventory service validates that it is a future instant when creating the hold but does not impose a maximum, minimum, or default lifetime.
- Releasing, consuming, or automatically processing expired holds remains outside this change; those lifecycle capabilities use the expiry time stored with the reservation.
- Existing reservation records remain valid historical data. The feature removes the need for a separate checkout-reservation-request record without discarding the reservations or their audit history.

## Dependencies

- The existing reservation creation capability continues to validate item availability, prevent over-reservation, and keep inventory changes atomic.
- The order service supplies a valid, unambiguous expiry time with every new reservation request.
- A reservation-lifecycle capability will use the retained expiry time to release, consume, or otherwise process holds when needed.
