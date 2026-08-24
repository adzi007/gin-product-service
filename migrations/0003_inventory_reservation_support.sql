-- Adds the reservation lifecycle columns, CHECK constraints and idempotency
-- support required by the inventory & reservation service
-- (specs/inventory-reservation.md, issue.md Step 0).
--
-- All statements are idempotent so the file can be re-run safely.
--
-- Idempotency design note (spec Sections 9 and 26): an order legitimately has
-- MULTIPLE reservation rows (one per line item, all ACTIVE), so a unique index
-- on reservations(order_id) is not viable. Instead a dedicated idempotency_keys
-- table uses order_id as its primary key: the create-reservation use case
-- inserts the order_id with ON CONFLICT DO NOTHING, so only the first request
-- for an order proceeds and retries (including concurrent races) simply return
-- the existing reservations. A failed transaction rolls the key back too, so a
-- corrected retry can still succeed.

-- 1. reservation_status enum (ACTIVE / COMPLETED / CANCELLED / EXPIRED).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'reservation_status') THEN
        CREATE TYPE reservation_status AS ENUM ('ACTIVE', 'COMPLETED', 'CANCELLED', 'EXPIRED');
    END IF;
END
$$;

-- 2. reservations lifecycle columns (spec Section 2.5). Existing rows (if any)
-- default to ACTIVE, which is correct for this feature's rollout.
ALTER TABLE reservations ADD COLUMN IF NOT EXISTS status reservation_status NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE reservations ADD COLUMN IF NOT EXISTS released_at timestamptz;

-- 3. CHECK constraints (spec Section 23). PostgreSQL has no
-- "ADD CONSTRAINT IF NOT EXISTS", so each is guarded by a DO block.
--
-- NOTE: a CHECK (quantity > 0) on stock_moves is intentionally NOT added here.
-- The product module's AdjustVariantStock (productRepo.go) records the ADJUST
-- *delta* in stock_moves.quantity, which is legitimately 0 or negative when
-- stock is lowered, so a strict positive constraint would break existing
-- behavior. quantity > 0 IS enforced for reservations (and in the inventory
-- use-case layer for stock moves).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_inventory_levels_available_qty_nonneg') THEN
        ALTER TABLE inventory_levels ADD CONSTRAINT chk_inventory_levels_available_qty_nonneg CHECK (available_qty >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_inventory_levels_reserved_qty_nonneg') THEN
        ALTER TABLE inventory_levels ADD CONSTRAINT chk_inventory_levels_reserved_qty_nonneg CHECK (reserved_qty >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_reservations_quantity_positive') THEN
        ALTER TABLE reservations ADD CONSTRAINT chk_reservations_quantity_positive CHECK (quantity > 0);
    END IF;
END
$$;

-- 4. Idempotency keys: at most one reservation batch per order.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    order_id   uuid primary key,
    created_at timestamptz not null default now()
);
