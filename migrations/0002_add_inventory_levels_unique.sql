-- Ensures a unique (inventory_item_id, location_id) pair on inventory_levels so
-- stock adjustments can upsert with ON CONFLICT. Applied idempotently so it is
-- safe to run on environments where the index/constraint already exists.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_levels_item_location_key
    ON inventory_levels (inventory_item_id, location_id);
