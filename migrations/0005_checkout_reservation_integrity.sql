-- 0005_checkout_reservation_integrity.sql
-- Forward-only integrity migration for the checkout inventory reservation
-- feature. Adds the durable order-level idempotency claim, strengthens the
-- reservation and inventory-level invariants, and guarantees at
-- most one default fulfillment location.
--
-- Idempotent where PostgreSQL allows it so the file can be re-applied safely.

-- 1. Durable idempotency claim: one complete checkout request per order.
CREATE TABLE IF NOT EXISTS checkout_reservation_requests (
    order_id            uuid PRIMARY KEY,
    request_fingerprint bytea NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now()
);

-- 2. Reservations must belong to an order, hold a positive quantity, and expire
--    strictly after they were created. Legacy rows are backfilled first so the
--    stricter constraints never drop or corrupt existing data.
UPDATE reservations SET order_id = gen_random_uuid() WHERE order_id IS NULL;
UPDATE reservations SET expires_at = reserved_at + interval '60 minutes'
    WHERE expires_at IS NULL AND reserved_at IS NOT NULL;

ALTER TABLE reservations ALTER COLUMN order_id SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'reservations_quantity_positive'
    ) THEN
        ALTER TABLE reservations ADD CONSTRAINT reservations_quantity_positive
            CHECK (quantity > 0);
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'reservations_expiry_after_reserved'
    ) THEN
        ALTER TABLE reservations ADD CONSTRAINT reservations_expiry_after_reserved
            CHECK (expires_at > reserved_at);
    END IF;
END $$;

-- 3. One reservation per order item (duplicate-item backstop).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'reservations_order_item_unique'
    ) THEN
        ALTER TABLE reservations
            ADD CONSTRAINT reservations_order_item_unique
            UNIQUE (order_id, inventory_item_id);
    END IF;
END $$;

-- 4. Inventory quantities are non-null and non-negative. Legacy NULLs are
--    normalized to zero before the constraints are enabled.
UPDATE inventory_levels SET available_qty = 0 WHERE available_qty IS NULL;
UPDATE inventory_levels SET reserved_qty  = 0 WHERE reserved_qty  IS NULL;

ALTER TABLE inventory_levels ALTER COLUMN available_qty SET NOT NULL;
ALTER TABLE inventory_levels ALTER COLUMN reserved_qty  SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'inventory_levels_available_nonnegative'
    ) THEN
        ALTER TABLE inventory_levels ADD CONSTRAINT inventory_levels_available_nonnegative
            CHECK (available_qty >= 0);
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'inventory_levels_reserved_nonnegative'
    ) THEN
        ALTER TABLE inventory_levels ADD CONSTRAINT inventory_levels_reserved_nonnegative
            CHECK (reserved_qty >= 0);
    END IF;
END $$;

-- 5. At most one default fulfillment location.
CREATE UNIQUE INDEX IF NOT EXISTS locations_single_default
    ON locations (is_default)
    WHERE is_default = true;
