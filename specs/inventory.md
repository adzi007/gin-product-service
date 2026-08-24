# Task: Inventory & Reservation Specification

## Ocerview

This is an inventory management that support a product microservice for updating stock through other service (order, inventory, purchasing, etc)

## API Contract

### 1. `POST /v1/inventory/stock-moves`

Executes an inventory move in `stock_moves` and  recalculates `inventory_levels` atomically inside a single database transaction.

**Request Body:**

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


**Response (`200 OK`):**

```json
{
  "status": "success",
  "data": {
    "move_id": "uuid",
    "variant_id": "uuid",
    "from_location_id": "uuid",
    "to_location_id": "uuid",
    "quantity": 10,
    "reason": "string",
  }
}

```

### 1.1 Busines requirement

1. **MoveType `IN`:** Increases `inventory_levels.available_qty` at `to_location_id`.
2. **MoveType `OUT`:** Decreases `inventory_levels.available_qty` at `from_location_id`. Requires `available_qty >= requested_qty`.
3. **MoveType `TRANSFER`:** Decreases `available_qty` at `from_location_id` and increases `available_qty` at `to_location_id`.
4. **MoveType `RESERVE`:** Shifts quantity from `available_qty` to `reserved_qty` at the specified `location_id`. Fails if `available_qty < requested_qty`.
5. **MoveType `UNRESERVE`:** Shifts quantity from `reserved_qty` back to `available_qty`.
5. **MoveType `ADJUST`:** Increases or decrease `inventory_levels.available_qty` by comparing the user input and the current `inventory_levels.available_qty` 


### 2. `POST /v1/inventory/reservations/`

Place new reservation `reservations` with multiple inventory item with the same order_id item atomically inside a single database transaction.

**Request Body:**

```json
{
  "order_id": "uuid",
  "expires_at": "timestamptz | null",
  "items": [
    { "variant_id": "uuid", "location_id": "uuid", "quantity": 2 },
    { "variant_id": "uuid", "location_id": "uuid", "quantity": 1 }
  ]
}

```

**Response (`200 OK`):**

```json
// I have no idea what should I response

```

The logic flow 
- Insert into `reservation`
- Update `inventory_levels.reserved_qty` with `request.quantity`, and `inventory_levels.available_qty` with `available_qty` - `request.quantity` where `inventory_levels.


### 3. `PUT /v1/inventory/reservations/:id/complete`

Order service complete reservation reservation

### 4. `PUT /v1/inventory/reservations/:id/cancel`

Order service cancel reservation

