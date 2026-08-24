## API Contract

#### `POST /v1/inventory/stock-moves`

Executes an inventory move and recalculates `inventory_levels` atomically inside a single database transaction.

**Request Body:**

```json
{
  "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
  "from_location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
  "to_location_id": "",
  "move_type": "IN",
  "quantity": 100,
  "reason": "Initial warehouse stock receipt"
}

```

```json
{
  "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
  "from_location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
  "to_location_id": "019154a1-8d2b-7c0a-9e12-32b001010088",
  "move_type": "TRANSFER",
  "quantity": 100,
  "reason": "Relocation place"
}

```

```json
{
  "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
  "from_location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
  "to_location_id": "",
  "move_type": "ADJUST",
  "quantity": 100,
  "reason": "Admin corection or other reason"
}

```

**Response (`200 OK`):**

```json
{
  "status": "success",
  "data": {
    "move_id": "019154a1-8d2b-7c0a-9e12-32b001010888",
    "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
    "location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
    "available_qty": "100",
    "reserved_qty": "0"
  }
}

```

#### `POST /v1/inventory/reservations`

Executes an reservation `reservations` atomically inside a single database transaction.

**Request Body:**

```json
{
  "inventory_item_id": "...",
  "location_id": "...",
  "quantity": 2,
  "order_id": "...",
  "expires_at": "..."
}

```

**Response (`200 OK`):**

```json
{
  "status": "success",
  "data": {
    "id": "...",
    "inventory_item_id": "...",
    "location_id": "...",
    "quantity": 2,
    "status": "ACTIVE",
    "expires_at": "..."
  }
}

```

## 5. Business Logic 

### 5.7 Stock Movement & Reservation Invariants

1. **MoveType `IN`:** Increases `inventory_levels.available_qty` at `to_location_id`.
2. **MoveType `OUT`:** Decreases `inventory_levels.available_qty` at `from_location_id`. Requires `available_qty >= requested_qty`.
3. **MoveType `TRANSFER`:** Decreases `available_qty` at `from_location_id` and increases `available_qty` at `to_location_id`.
4. **MoveType `RESERVE`:** Shifts quantity from `available_qty` to `reserved_qty` at the specified `location_id`. Fails if `available_qty < requested_qty`.
5. **MoveType `UNRESERVE`:** Shifts quantity from `reserved_qty` back to `available_qty`.
5. **MoveType `ADJUST`:** Increases or decrease `inventory_levels.available_qty` by comparing the user input and the current `inventory_levels.available_qty` 