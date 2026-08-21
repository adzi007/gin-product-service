-- Adds display-order position to variants (required for the variant reorder
-- endpoints). Applied idempotently so it is safe to run on environments where
-- the column already exists.
ALTER TABLE variants ADD COLUMN IF NOT EXISTS position integer NOT NULL DEFAULT 0;
