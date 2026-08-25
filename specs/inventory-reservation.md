# Inventory & Reservation Service Specification
## 1. Overview
Implement inventory management functionality inside the existing Product Microservice.
The inventory functionality is consumed by other backend services such as:
* Order
* Purchasing
* Warehouse/Inventory
* Other internal services

These inventory APIs are **service-to-service APIs** and are **not intended to be called directly by the frontend**.

The implementation must use PostgreSQL transactions to guarantee atomicity and must prevent race conditions when multiple requests modify the same inventory simultaneously.

The primary cross-service identifier for inventory operations is:

```text
variant_id
```

The `inventory_item_id` is an internal inventory implementation detail and must **not** be required from other services.

The relationship is:

```text
products
    |
    +--- variants
            |
            +--- inventory_items
                    |
                    +--- inventory_levels
                    |
                    +--- reservations
                    |
                    +--- stock_moves
```
`inventory_items.variant_id` identifies which product variant the inventory item belongs to.

---

# 2. Existing Inventory Tables

## 2.1 `inventory_items`

```sql
inventory_items (
    id uuid primary key,
    variant_id uuid not null unique,
    description text,
    track_inventory boolean default true,
    created_at timestamptz default now()
)
```

Important rule:

```text
one variant = one inventory_item
```

Therefore, given a `variant_id`, the application must resolve the corresponding `inventory_item.id` internally.

---

## 2.2 `locations`

```sql
locations (
    id uuid primary key,
    name text not null,
    type text,
    address jsonb,
    is_default boolean,
    created_at timestamptz default now()
)
```

A location represents a warehouse, store, etc.

---

## 2.3 `inventory_levels`

```sql
inventory_levels (
    id uuid primary key,
    inventory_item_id uuid not null,
    location_id uuid not null,
    available_qty integer,
    reserved_qty integer,
    updated_at timestamptz default now()
)
```
Unique constraint:
```text
(inventory_item_id, location_id)
```

Meaning one inventory item has at most one inventory level per location.
Inventory quantities must never become negative.

Required invariant:
```text
available_qty >= 0
reserved_qty >= 0
```

---

## 2.4 `stock_moves`

```sql
stock_moves (
    id uuid primary key,
    inventory_item_id uuid not null,
    from_location_id uuid,
    to_location_id uuid,
    move_type stock_move_type not null,
    quantity integer,
    created_by uuid,
    reason text,
    created_at timestamptz default now()
)
```

Stock move types:

```text
IN
OUT
TRANSFER
ADJUST
RESERVE
UNRESERVE
```

For API purposes, `RESERVE` and `UNRESERVE` are not required for the reservation endpoint. Reservation creation, completion, cancellation, and expiration should be represented by the `reservations` table.

---

## 2.5 `reservations`

```sql
reservations (
    id uuid primary key,
    inventory_item_id uuid not null,
    location_id uuid not null,
    order_id uuid,
    quantity integer,
    reserved_at timestamptz default now(),
    expires_at timestamptz,
    status reservation_status not null,
    released_at timestamptz
)
```

Reservation statuses:

```text
ACTIVE
COMPLETED
CANCELLED
EXPIRED
```

The status values `COMPLETED` and `CANCELLED` are recommended instead of using a generic `RELEASED`, because the system needs to distinguish a successful order completion from cancellation.

---

# 3. General Rules

## 3.1 Cross-service identifier

Requests from other services must use:

```text
variant_id
```

Do not require:

```text
inventory_item_id
```

The inventory implementation must internally resolve:

```sql
SELECT id
FROM inventory_items
WHERE variant_id = $1
```

The `inventory_item_id` must remain an internal implementation detail.

---

## 3.2 Transaction requirement

The following operations must execute all related database modifications inside a single PostgreSQL transaction:

### Stock move

```text
INSERT stock_moves
+
UPDATE inventory_levels
```

### Reservation creation

```text
INSERT reservations
+
UPDATE inventory_levels
```

### Reservation completion

```text
UPDATE reservations
+
UPDATE inventory_levels
```

### Reservation cancellation

```text
UPDATE reservations
+
UPDATE inventory_levels
```

If any operation fails, the entire transaction must roll back.

---

## 3.3 Concurrency requirement

Inventory updates must be safe when multiple requests operate on the same inventory level concurrently.

Before changing an inventory level, lock the row using:

```sql
SELECT ...
FROM inventory_levels
WHERE inventory_item_id = $1
  AND location_id = $2
FOR UPDATE;
```

Do not read a quantity, calculate a new quantity in application memory, and then update it without locking the row.

The implementation must prevent:

```text
Request A: available = 10
Request B: available = 10

A reserves 8
B reserves 8

Result must NOT become:
available = -6
```

The database transaction must serialize conflicting updates.

---

# 4. API 1 — Create Stock Move

## Endpoint

```http
POST /v1/inventory/stock-moves
```

This endpoint records a physical or administrative stock movement and updates the affected inventory level(s) atomically.

---

## 4.1 Request

```json
{
  "variant_id": "uuid",
  "move_type": "IN | OUT | TRANSFER | ADJUST",
  "quantity": 10,
  "from_location_id": "uuid | null",
  "to_location_id": "uuid | null",
  "reason": "string",
  "created_by": "uuid"
}
```

---

## 4.2 Validation

### Common validation

* `variant_id` is required.
* `move_type` is required.
* `quantity` must be greater than `0`.
* `variant_id` must exist.
* An `inventory_item` must exist for the variant.
* If `inventory_items.track_inventory = false`, inventory quantity updates may be skipped according to application policy; however, the behavior must be consistent and explicitly implemented.

### `IN`

Required:

```text
to_location_id
quantity > 0
```

Not required:

```text
from_location_id
```

### `OUT`

Required:

```text
from_location_id
quantity > 0
```

Not required:

```text
to_location_id
```

Must fail when:

```text
available_qty < quantity
```

### `TRANSFER`

Required:

```text
from_location_id
to_location_id
quantity > 0
```

`from_location_id` and `to_location_id` must be different.

The source location must have:

```text
available_qty >= quantity
```

### `ADJUST`

`ADJUST` must represent the desired final physical available quantity rather than a relative quantity delta.

Therefore, the request should use:

```json
{
  "variant_id": "uuid",
  "move_type": "ADJUST",
  "quantity": 100,
  "to_location_id": "uuid",
  "reason": "Physical stock count"
}
```

For `ADJUST`:

```text
quantity = desired/final available quantity
```

The implementation must:

1. Lock the inventory level.
2. Read current `available_qty`.
3. Calculate:

```text
difference = requested_quantity - current_available_qty
```

4. Update `available_qty` to the requested quantity.
5. Record the adjustment in `stock_moves`.

Example:

```text
Current available = 80
Requested quantity = 100

Difference = +20

Final available = 100
```

Another example:

```text
Current available = 80
Requested quantity = 70

Difference = -10

Final available = 70
```

`ADJUST` must never produce a negative quantity.

---

# 5. Stock Move Behavior

## 5.1 `IN`

Example:

```text
available = 10
IN quantity = 5
```

Result:

```text
available = 15
```

Transaction:

```text
BEGIN

resolve inventory_item_id from variant_id

lock inventory level

available_qty += quantity

insert stock_move

COMMIT
```

---

## 5.2 `OUT`

Example:

```text
available = 10
OUT quantity = 4
```

Result:

```text
available = 6
```

If:

```text
available = 3
OUT quantity = 4
```

the transaction must fail and roll back.

---

## 5.3 `TRANSFER`

Example:

```text
Warehouse A available = 20
Warehouse B available = 5

transfer 7 from A to B
```

Result:

```text
Warehouse A available = 13
Warehouse B available = 12
```

Both inventory-level rows must be locked before modification.

The operation must be atomic:

```text
either both locations are updated
or neither location is updated
```

To minimize deadlock risk when transferring between two locations, locks should be acquired in a deterministic order, such as ordering by `inventory_levels.id` or `location_id`.

---

## 5.4 `ADJUST`

`ADJUST` sets the final available quantity.

Example:

```text
Current = 50
Requested = 45
```

Result:

```text
Available = 45
```

The stock move record should retain the requested quantity and reason.

The implementation should also be able to determine the adjustment delta:

```text
delta = requested - old_available
```

---

# 6. Stock Move Response

Return:

```http
200 OK
```

```json
{
  "status": "success",
  "data": {
    "move_id": "uuid",
    "variant_id": "uuid",
    "move_type": "TRANSFER",
    "from_location_id": "uuid",
    "to_location_id": "uuid",
    "quantity": 10,
    "reason": "Warehouse transfer",
    "created_at": "2026-08-25T00:00:00Z"
  }
}
```

For `IN`, `from_location_id` should be `null`.

For `OUT`, `to_location_id` should be `null`.

---

# 7. API 2 — Create Reservations

## Endpoint

```http
POST /v1/inventory/reservations
```

This endpoint creates reservations for multiple variants belonging to the same order.

All reservation records and all inventory-level changes must occur inside one transaction.

If any item cannot be reserved, **nothing should be committed**.

---

# 8. Reservation Request

The caller does not supply a location. The inventory service chooses the
location to reserve from for each item (see
`specs/remove-location-from-reservation.md`); each reservation row reports the
`location_id` that was actually used.

```json
{
  "order_id": "uuid",
  "expires_at": "2026-08-25T01:00:00Z",
  "items": [
    {
      "variant_id": "uuid",
      "quantity": 2
    },
    {
      "variant_id": "uuid",
      "quantity": 1
    }
  ]
}
```

---

# 9. Reservation Validation

### `order_id`

Required.

The same order must not create multiple active reservations unless the business explicitly allows reservation modification/retry.

The implementation must be idempotent.

Recommended behavior:

```text
same order_id + same operation/idempotency key
    => return the existing reservation result
```

Do not create duplicate reservations if a caller retries a request because of a network timeout.

### `items`

Required.

Must contain at least one item.

Each item:

```text
variant_id   required
quantity     integer > 0
```

The implementation must verify:

```text
variant exists
inventory_item exists
inventory_level exists (for at least one location; the location is chosen by the server)
```

---

# 10. Reservation Transaction

For every requested item:

1. Resolve `inventory_item_id` from `variant_id`.
2. Choose the location to reserve from and lock the `inventory_levels` row
   using `SELECT ... FOR UPDATE` in one statement. Single-location fulfillment:
   exactly one location is picked per item — the default location if it has
   enough `available_qty`, otherwise the location with the highest
   `available_qty`, tie-broken by `location_id ASC` (see
   `specs/remove-location-from-reservation.md`). If no single location covers
   the quantity, the reservation fails with `ERR_INSUFFICIENT_STOCK`.
3. Check:

```text
available_qty >= requested quantity
```

4. Update:

```text
available_qty = available_qty - quantity
reserved_qty = reserved_qty + quantity
```

5. Insert:

```text
reservations.status = ACTIVE
```

6. Commit only after all requested items succeed.

Pseudo-flow:

```text
BEGIN

for each item:
    resolve inventory_item_id
    select + lock inventory_level (best location, FOR UPDATE)

    if available_qty < quantity:
        ROLLBACK

    available_qty -= quantity
    reserved_qty += quantity

    insert reservation

COMMIT
```

---

# 11. Reservation Response

Return:

```http
201 Created
```

```json
{
  "status": "success",
  "data": {
    "order_id": "uuid",
    "reservations": [
      {
        "reservation_id": "uuid",
        "variant_id": "uuid",
        "location_id": "uuid",
        "quantity": 2,
        "status": "ACTIVE",
        "reserved_at": "2026-08-25T00:00:00Z",
        "expires_at": "2026-08-25T01:00:00Z"
      },
      {
        "reservation_id": "uuid",
        "variant_id": "uuid",
        "location_id": "uuid",
        "quantity": 1,
        "status": "ACTIVE",
        "reserved_at": "2026-08-25T00:00:00Z",
        "expires_at": "2026-08-25T01:00:00Z"
      }
    ]
  }
}
```

The response should provide the reservation IDs generated by the inventory service.

---

# 12. Reservation Completion

## Endpoint

```http
PUT /v1/inventory/reservations/:orderId/complete
```

The Order Service calls this after successful payment/order completion.

The endpoint identifies all active reservations belonging to the order.

No variant IDs are required in the request.

Example:

```http
PUT /v1/inventory/reservations/0198-order-id/complete
```

---

# 13. Completion Behavior

The transaction must:

1. Find all `ACTIVE` reservations for the order.
2. Lock the reservation rows.
3. Lock the corresponding inventory-level rows.
4. For each reservation:

```text
reserved_qty -= reservation.quantity
```

Do **not** increase `available_qty`.

Why:

The inventory was already moved from:

```text
available
```

to:

```text
reserved
```

when the reservation was created.
When the order is completed, the reserved quantity becomes consumed/sold.

Therefore:

```text
Reservation created:

available 100
reserved   0

Reserve 5:

available 95
reserved   5

Complete:

available 95
reserved   0
```

5. Update:

```text
status = COMPLETED
```

6. Update:

```text
released_at = now()
```

7. Commit.

The operation must be idempotent.

Calling complete again for an already completed reservation must not consume inventory a second time.

---

# 14. Completion Response

Return:

```http
200 OK
```

```json
{
  "status": "success",
  "data": {
    "order_id": "uuid",
    "status": "COMPLETED",
    "reservations": [
      {
        "reservation_id": "uuid",
        "variant_id": "uuid",
        "quantity": 2,
        "status": "COMPLETED"
      }
    ]
  }
}
```

If the order has no active reservation:

* Return a successful idempotent response when the order was already completed.
* Return an appropriate not-found/conflict error when no reservation has ever existed.

---

# 15. Reservation Cancellation

## Endpoint

```http
PUT /v1/inventory/reservations/:orderId/cancel
```

Used when an order is cancelled before fulfillment/payment completion.

---

# 16. Cancellation Behavior

The transaction must:

1. Find all `ACTIVE` reservations for the order.
2. Lock the reservation rows.
3. Lock the corresponding inventory-level rows.
4. For each reservation:

```text
reserved_qty -= reservation.quantity
available_qty += reservation.quantity
```

5. Update:

```text
status = CANCELLED
```

6. Update:

```text
released_at = now()
```

7. Commit.

Example:

```text
Before cancellation:

available = 95
reserved  = 5

After cancellation:

available = 100
reserved  = 0
```

This returns the reserved inventory to available inventory.
The operation must be idempotent.
Cancelling an already cancelled reservation must not restore inventory twice.

---

# 17. Cancellation Response

Return:

```http
200 OK
```

```json
{
  "status": "success",
  "data": {
    "order_id": "uuid",
    "status": "CANCELLED",
    "reservations": [
      {
        "reservation_id": "uuid",
        "variant_id": "uuid",
        "quantity": 2,
        "status": "CANCELLED"
      }
    ]
  }
}
```

---

# 18. Reservation Expiration

If `expires_at` is present and the reservation remains `ACTIVE` after that timestamp, it should be treated as expired.

Expiration behavior is equivalent to cancellation:

```text
reserved_qty -= quantity
available_qty += quantity
status = EXPIRED
released_at = now()
```

Expiration should be implemented by a background job/worker or a scheduled process.

The expiration operation must also be transactional and idempotent.

---

# 19. Important Inventory Invariants

The implementation must always preserve:

```text
available_qty >= 0
reserved_qty >= 0
```

A reservation must never succeed when:

```text
available_qty < requested quantity
```

A cancellation/expiration must never cause:

```text
reserved_qty < 0
```

A completion must never cause:

```text
reserved_qty < 0
```

---

# 20. Difference Between Reservation and Stock Movement

Do not treat reservation as physical stock movement.

Reservation means:

```text
available → reserved
```

It does not mean the item physically left the warehouse.

Completion means:

```text
reserved → consumed
```

Cancellation/expiration means:

```text
reserved → available
```

Physical movement such as:

```text
warehouse receiving
warehouse transfer
shipping
stock count adjustment
```

is represented through `stock_moves`.

Therefore:

```text
Stock Move:
    Physical inventory change

Reservation:
    Allocation/commitment of available inventory
```

---

# 21. Recommended Error Handling

Use appropriate HTTP status codes.

## `400 Bad Request`

Malformed request:

```text
invalid UUID
quantity <= 0
invalid move_type
missing required location
from_location_id == to_location_id
```

## `404 Not Found`

Referenced entity does not exist:

```text
variant not found
inventory item not found
location not found
inventory level not found
```

## `409 Conflict`

Business conflict:

```text
insufficient inventory
reservation already completed
reservation already cancelled
duplicate active reservation
```

## `422 Unprocessable Entity`

Optional if the existing API conventions use it for semantic validation.

## `500 Internal Server Error`

Unexpected database or infrastructure failure.

---

# 22. Concurrency and Lock Ordering

When locking multiple inventory levels in one transaction, the implementation must use a deterministic order.

Reservation creation locks one level per item (the location is chosen by the server at lock time, see `specs/remove-location-from-reservation.md`), so requested items are sorted by:

```text
inventory_item_id ASC
```

and locked in that order. Within an item, the choice of location is deterministic (`location_id ASC` tie-break).

Reservation completion, cancellation, and expiration already know the `location_id` from the stored reservation row and order by:

```text
inventory_item_id ASC
location_id ASC
```

This reduces the possibility of deadlocks when two concurrent requests reserve/transfer the same inventory levels in a different order.

---

# 23. Database-Level Constraints

The implementation should use database constraints whenever possible.

Recommended constraints:

```text
inventory_items.variant_id UNIQUE

inventory_levels(inventory_item_id, location_id) UNIQUE

available_qty >= 0

reserved_qty >= 0

quantity > 0
```

Where practical, use PostgreSQL `CHECK` constraints.

Example:

```sql
CHECK (available_qty >= 0)

CHECK (reserved_qty >= 0)
```

And:

```sql
CHECK (quantity > 0)
```

for reservations and stock moves.

---

# 24. Service Boundary

Other services should send:

```text
variant_id
```

not:

```text
inventory_item_id
```

Example:

```json
{
  "variant_id": "0198...",
  "quantity": 2
}
```

The Product/Inventory service internally resolves:

```text
variant_id
    ↓
inventory_items.id
    ↓
inventory_levels
```

This prevents internal inventory database identifiers from leaking across service boundaries.

---

# 25. Recommended Request Flow

## Order creation

```text
Frontend
    ↓
Order Service
    ↓
POST /v1/inventory/reservations
    ↓
Product/Inventory Service
    ↓
Reserve inventory
```

Successful result:

```text
Order = PENDING_PAYMENT
Inventory = RESERVED
```
---

## Payment success

```text
Frontend
    ↓
Order Service
    ↓
PUT /v1/inventory/reservations/{orderId}/complete
    ↓
Product/Inventory Service
    ↓
Complete reservation
```

Result:

```text
Order = PAID
Inventory reservation = COMPLETED
```

---

## Order cancellation

```text
Order Service
    ↓
PUT /v1/inventory/reservations/{orderId}/cancel
    ↓
Product/Inventory Service
    ↓
Cancel reservation
```

Result:

```text
Order = CANCELLED
Inventory = returned to AVAILABLE
```

---

# 26. Idempotency

Because these APIs are service-to-service APIs and requests can be retried, reservation creation should support idempotency.

Recommended approach:

```http
Idempotency-Key: <unique-operation-id>
```

The server should persist the operation key and result so that retries do not create duplicate reservations.

At minimum, the system must ensure that the same order cannot accidentally reserve the same stock twice due to a retry.

---

# 27. Implementation Requirements

The implementation must:

1. Follow the existing project's architecture and conventions.
2. Use the existing PostgreSQL repository/database abstraction.
3. Use database transactions for every operation described above.
4. Use `SELECT ... FOR UPDATE` when changing inventory quantities.
5. Never modify inventory quantities without a transaction.
6. Resolve `inventory_item_id` internally from `variant_id`.
7. Return meaningful HTTP errors.
8. Validate all UUIDs and quantities.
9. Prevent negative inventory quantities.
10. Make reservation completion and cancellation idempotent.
11. Preserve an auditable reservation history.
12. Avoid partial success when reserving multiple items.
13. Add unit tests for business logic.
14. Add repository/integration tests for transaction and concurrency-sensitive behavior.
15. Add HTTP handler tests for success and error cases.

---

# 28. Minimum Test Scenarios

The implementation must test at least the following.

## Stock moves

### IN

```text
available = 10
IN 5
=> available = 15
```

### OUT

```text
available = 10
OUT 5
=> available = 5
```

### OUT insufficient stock

```text
available = 5
OUT 10
=> transaction fails
=> available remains 5
=> no stock_move inserted
```

### TRANSFER

```text
A = 20
B = 10

transfer 5 A → B

A = 15
B = 15
```

### ADJUST increase

```text
current = 10
adjust = 15
=> available = 15
```

### ADJUST decrease

```text
current = 10
adjust = 5
=> available = 5
```

---

## Reservations

### Successful multiple-item reservation

```text
Variant A = 10
Variant B = 5

Reserve:
A = 3
B = 2

Result:

A available = 7
A reserved  = 3

B available = 3
B reserved  = 2
```

### One item insufficient

If:

```text
A available = 10
B available = 1

Request:
A = 3
B = 2
```

The entire transaction must fail.

Final state:

```text
A available = 10
A reserved  = 0

B available = 1
B reserved  = 0
```

No partial reservation is allowed.

---

## Completion

```text
available = 7
reserved = 3

complete reservation

available = 7
reserved = 0
status = COMPLETED
```

Calling completion twice must not modify inventory twice.

---

## Cancellation

```text
available = 7
reserved = 3

cancel reservation

available = 10
reserved = 0
status = CANCELLED
```

Calling cancellation twice must not restore stock twice.

---

## Concurrent reservation

Given:

```text
available = 10
```

Run concurrently:

```text
request A reserves 7
request B reserves 7
```

Expected:

```text
only one request succeeds
the other fails with insufficient inventory
```

Final state:

```text
available = 3
reserved = 7
```

The final state must never be:

```text
available = -4
```

and must never reserve 14 units.

---

# 29. Important Design Principle

The inventory system should treat the following as separate concepts:

```text
Product identity
    = variant_id

Inventory identity
    = inventory_item_id

Location identity
    = location_id

Order identity
    = order_id

Reservation identity
    = reservation_id

Physical stock event
    = stock_move_id
```

Cross-service APIs should use stable business/domain identifiers such as:

```text
variant_id
order_id
location_id
```

Internal database identifiers such as:

```text
inventory_item_id
```

should remain internal unless there is a deliberate reason to expose them.
