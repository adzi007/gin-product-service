# Data Model: Checkout Inventory Reservation

## CheckoutReservationRequest

Durable idempotency claim for one checkout order; this table is new.

| Field | Type | Rules |
|---|---|---|
| `order_id` | UUID | Primary key; caller-supplied valid UUID; one complete checkout request per order. |
| `request_fingerprint` | `bytea` | Required SHA-256 over canonical sorted `(variant_id, quantity)` pairs. |
| `created_at` | `timestamptz` | Database-assigned creation time. |

An existing order with the same fingerprint returns persisted reservations. A different
fingerprint is `ERR_RESERVATION_CONFLICT` and makes no inventory change.

## Reservation

The existing table is extended with constraints and a linked stock movement.

| Field | Type | Rules |
|---|---|---|
| `id` | UUID | UUIDv7 generated in the use case. |
| `inventory_item_id` | UUID | Required FK; resolved internally from caller variant ID. |
| `location_id` | UUID | Required FK; the configured default location. |
| `order_id` | UUID | Required logical FK to the idempotency claim. |
| `quantity` | integer / `domain.Quantity` | Required whole number `> 0`. |
| `reserved_at` | `timestamptz` | Database-assigned once per transaction. |
| `expires_at` | `timestamptz` | Exactly `reserved_at + 60 minutes`; later than `reserved_at`. |
| `status` | reservation enum | Created as `ACTIVE` only. |
| `released_at` | nullable `timestamptz` | Reserved for lifecycle work. |

Unique `(order_id, inventory_item_id)` prevents duplicate reservation items. Lifecycle:

```text
ACTIVE --future release/cancel--> RELEASED
ACTIVE --future expiry worker----> EXPIRED
```

This feature creates only `ACTIVE`.

## ReservationRequestItem

Transient domain value:

| Field | Type | Rules |
|---|---|---|
| `VariantID` | UUID | Public `items[].id`; unique within request. |
| `Quantity` | `domain.Quantity` | Public `items[].qty`; positive whole number. |

Callers never supply inventory-item or location IDs. Fingerprints/leases sort by variant
UUID; PostgreSQL locks sort resolved inventory item UUIDs.

## Inventory item and level

| Entity | Reservation rule |
|---|---|
| `InventoryItem` | Resolves from a non-deleted variant and has `track_inventory=true`. |
| `InventoryLevel` | Exists at the one default location and is locked before checking. Both stored quantities are non-null integers `>= 0`. |

On success: `available_after = available_before - quantity` and
`reserved_after = reserved_before + quantity`. Insufficient pre-update availability
rejects the entire request.

## StockMove

One existing-table row is inserted per reservation.

| Field | Value |
|---|---|
| `id` | New UUIDv7 |
| `inventory_item_id` | Item resolved from the public variant |
| `from_location_id` | Selected default location |
| `to_location_id` | `NULL` |
| `move_type` | `RESERVE` |
| `quantity` | Held positive quantity |
| `reservation_id` | New nullable FK, populated and unique when present |

The direct link supports later reconciliation, release, and consumption without parsing a
human-readable reason.

## Location and errors

`locations.is_default=true` identifies the chosen location; a partial unique index
allows at most one default. No default/eligible level rejects the complete request.
Stable errors cover validation, unknown/missing/non-reservable inventory, insufficient
stock, idempotency conflict, in-progress reservation, and unavailable coordination.
Item failures identify only the public variant ID.
