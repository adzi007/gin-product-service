-- Creates the product_reviews table for the reviews & ratings module.
-- The application supplies the UUIDv7 id (per repo convention), so no
-- gen_random_uuid() default is used. updated_at is bumped by application code
-- on update (matching how products and variants handle it).
CREATE TABLE IF NOT EXISTS product_reviews (
    id           uuid PRIMARY KEY,
    product_id   uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    variant_id   uuid REFERENCES variants(id) ON DELETE SET NULL,
    user_id      uuid NOT NULL,
    display_name text NOT NULL,
    rating       integer NOT NULL CHECK (rating BETWEEN 1 AND 5),
    title        text,
    comment      text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_reviews_product_date
    ON product_reviews (product_id, created_at);

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_product_review
    ON product_reviews (user_id, product_id);
