-- Drops the reservations_active_order_id_key index created by an earlier draft
-- of 0003. That partial unique index only allowed a single ACTIVE reservation
-- row per order, which conflicts with the multi-line-item reservation model
-- (spec Sections 8/11). Idempotency is now enforced by the idempotency_keys
-- table defined in the corrected 0003. Safe to run on environments that never
-- created the index (IF NOT EXISTS-equivalent via DROP INDEX IF EXISTS).
DROP INDEX IF EXISTS reservations_active_order_id_key;
