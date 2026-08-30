-- Denormalize the product list aggregates (price range + rating) onto
-- `products` and keep them in sync via triggers on `variants` and
-- `product_reviews`. This removes the `price_stats` LATERAL join from the
-- product list query so it becomes a flat, indexable SELECT on `products`.

-- 2.1 Add denormalized columns.
ALTER TABLE products
  ADD COLUMN price_min numeric(12,2) NOT NULL DEFAULT 0,
  ADD COLUMN price_max numeric(12,2) NOT NULL DEFAULT 0,
  ADD COLUMN rating_avg numeric(3,2) NOT NULL DEFAULT 0,
  ADD COLUMN rating_count integer NOT NULL DEFAULT 0;

-- 2.2 Backfill existing rows (run before enabling the triggers in production
-- traffic). Products with no variants/reviews correctly stay at DEFAULT 0.
UPDATE products p
SET price_min = COALESCE(v.min_price, 0),
    price_max = COALESCE(v.max_price, 0)
FROM (
  SELECT product_id, MIN(price) AS min_price, MAX(price) AS max_price
  FROM variants
  WHERE is_deleted = false
  GROUP BY product_id
) v
WHERE v.product_id = p.id;

UPDATE products p
SET rating_avg = COALESCE(r.avg_rating, 0),
    rating_count = COALESCE(r.cnt, 0)
FROM (
  SELECT product_id, ROUND(AVG(rating)::numeric, 2) AS avg_rating, COUNT(*) AS cnt
  FROM product_reviews
  GROUP BY product_id
) r
WHERE r.product_id = p.id;

-- 2.3 Trigger functions and triggers.
CREATE OR REPLACE FUNCTION refresh_product_price_stats(p_product_id uuid)
RETURNS void AS $$
BEGIN
  UPDATE products
  SET price_min = COALESCE((
        SELECT MIN(price) FROM variants
        WHERE product_id = p_product_id AND is_deleted = false
      ), 0),
      price_max = COALESCE((
        SELECT MAX(price) FROM variants
        WHERE product_id = p_product_id AND is_deleted = false
      ), 0)
  WHERE id = p_product_id;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION trg_variants_price_stats()
RETURNS trigger AS $$
BEGIN
  PERFORM refresh_product_price_stats(COALESCE(NEW.product_id, OLD.product_id));
  -- product_id can change only in pathological cases; guard anyway.
  IF TG_OP = 'UPDATE' AND OLD.product_id IS DISTINCT FROM NEW.product_id THEN
    PERFORM refresh_product_price_stats(OLD.product_id);
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER variants_price_stats_sync
AFTER INSERT OR UPDATE OF price, is_deleted, product_id OR DELETE ON variants
FOR EACH ROW EXECUTE FUNCTION trg_variants_price_stats();

CREATE OR REPLACE FUNCTION refresh_product_rating_stats(p_product_id uuid)
RETURNS void AS $$
BEGIN
  UPDATE products
  SET rating_avg = COALESCE((
        SELECT ROUND(AVG(rating)::numeric, 2)
        FROM product_reviews WHERE product_id = p_product_id
      ), 0),
      rating_count = (
        SELECT COUNT(*) FROM product_reviews WHERE product_id = p_product_id
      )
  WHERE id = p_product_id;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION trg_reviews_rating_stats()
RETURNS trigger AS $$
BEGIN
  PERFORM refresh_product_rating_stats(COALESCE(NEW.product_id, OLD.product_id));
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER product_reviews_rating_stats_sync
AFTER INSERT OR UPDATE OF rating, product_id OR DELETE ON product_reviews
FOR EACH ROW EXECUTE FUNCTION trg_reviews_rating_stats();

-- 2.4 Indexes.
CREATE INDEX idx_products_status_price_min   ON products (status, price_min)          WHERE status = 'active';
CREATE INDEX idx_products_status_rating_avg  ON products (status, rating_avg);
CREATE INDEX idx_products_status_rating_cnt  ON products (status, rating_count DESC)   WHERE status = 'active';
