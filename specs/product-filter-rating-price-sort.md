# Spec: Rating Filter + Price/Popularity Sorting for Product List Endpoint

## 1. Goal

Add to `GET /products`:
- `rating` — filter by one or more rounded average ratings (e.g. `rating=3,4,5`)
- `sort_by=price` + `sort_dir=asc|desc` — cheapest / most expensive
- `sort_by=popularity` + `sort_dir=asc|desc` — most / least reviewed

Per the earlier discussion: stop computing price range and rating aggregates live via `JOIN`/`LATERAL` on every list request. Denormalize `price_min`, `price_max`, `rating_avg`, `rating_count` onto `products`, kept in sync via triggers on `variants` and `product_reviews`. This removes the `price_stats` LATERAL join entirely and makes the list query a flat, indexable `SELECT` on `products`.

This is a breaking internal change to `FindAll` — no API contract change beyond the two new params.

---

## 2. Database changes

### 2.1 Migration: add denormalized columns

```sql
ALTER TABLE products
  ADD COLUMN price_min numeric(12,2) NOT NULL DEFAULT 0,
  ADD COLUMN price_max numeric(12,2) NOT NULL DEFAULT 0,
  ADD COLUMN rating_avg numeric(3,2) NOT NULL DEFAULT 0,
  ADD COLUMN rating_count integer NOT NULL DEFAULT 0;
```

### 2.2 Backfill existing rows

Run once, after the column migration, before enabling triggers in production traffic:

```sql
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
```

Products with no variants or no reviews correctly stay at the `DEFAULT 0` from step 2.1 (no matching row in the `UPDATE ... FROM` subquery).

### 2.3 Trigger functions

```sql
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
  -- product_id can change only in pathological cases; guard anyway
  IF TG_OP = 'UPDATE' AND OLD.product_id IS DISTINCT FROM NEW.product_id THEN
    PERFORM refresh_product_price_stats(OLD.product_id);
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER variants_price_stats_sync
AFTER INSERT OR UPDATE OF price, is_deleted, product_id OR DELETE ON variants
FOR EACH ROW EXECUTE FUNCTION trg_variants_price_stats();
```

```sql
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
```

> Note: these triggers do a full re-aggregation per write. Fine at current review/variant volume. If review write volume becomes very high later, replace with incremental counters (`rating_count + 1`, running sum) or move the recompute to an async worker off a queue/outbox — flag this as a known future optimization, not needed now.

### 2.4 Indexes

```sql
CREATE INDEX idx_products_status_price_min   ON products (status, price_min)          WHERE status = 'active';
CREATE INDEX idx_products_status_rating_avg  ON products (status, rating_avg);
CREATE INDEX idx_products_status_rating_cnt  ON products (status, rating_count DESC)   WHERE status = 'active';
```

### 2.5 Remove `priceStatsJoin`

Once backfilled and triggers are live, `price_stats` LATERAL join in the repo is no longer needed for reads — `products.price_min` / `products.price_max` are read directly.

---

## 3. Domain changes (`domain` package)

```go
type ListProductParams struct {
	Search     string
	CategoryID int
	Status     string
	MinPrice   *decimal.Decimal
	MaxPrice   *decimal.Decimal
	Ratings    []int            // NEW: optional. Filters products whose ROUND(rating_avg) is in this set. Values expected 1-5, validated at handler layer.
	Page       int
	PerPage    int
	SortBy     string // whitelisted: "title", "created_at", "category_name", "price", "popularity"  (added "price", "popularity")
	SortDir    string // "asc" | "desc"
}
```

```go
type ProductPrices struct {
	StartPrice decimal.Decimal `json:"start_price"`
	MaxPrice   decimal.Decimal `json:"max_price"`
}

// NEW
type ProductRating struct {
	Average decimal.Decimal `json:"average"`
	Count   int             `json:"count"`
}

type ProductListItem struct {
	Product
	Category  ProductCategory   `json:"category"`
	Prices    ProductPrices     `json:"prices"`
	Rating    ProductRating     `json:"rating"`    // NEW
	Thumbnail *ProductThumbnail `json:"thumbnail"`
}
```

**Semantics of `Ratings []int`:** set-membership filter on the *rounded* average rating — `rating=3,4,5` returns products whose `ROUND(rating_avg)` is 3, 4, or 5. Not an "X stars & up" threshold filter. (Confirm this matches product intent; if the intended UX is "X stars & up," the WHERE clause changes to `rating_avg >= $min` instead of `ANY($ratings)` — flag this to the requester before implementing if ambiguous.)

---

## 4. Repository changes (`productRepo`)

### 4.1 `buildListConditions` — add rating filter

```go
if len(params.Ratings) > 0 {
    argIdx++
    args = append(args, params.Ratings) // pgx passes []int as int4[]
    conditions = append(conditions, fmt.Sprintf("ROUND(products.rating_avg) = ANY($%d)", argIdx))
}
```

Replace the existing price conditions to reference the denormalized columns directly instead of `price_stats.*`:

```go
if params.MinPrice != nil {
    argIdx++
    args = append(args, pgNumeric(*params.MinPrice))
    conditions = append(conditions, fmt.Sprintf("products.price_max >= $%d", argIdx))
}

if params.MaxPrice != nil {
    argIdx++
    args = append(args, pgNumeric(*params.MaxPrice))
    conditions = append(conditions, fmt.Sprintf("products.price_min <= $%d", argIdx))
}
```

### 4.2 `FindAll` — drop `price_stats` join, update SELECT and sort whitelist

**Count query** — no longer needs `priceStatsJoin`:

```go
countQuery := "SELECT COUNT(*) FROM products LEFT JOIN category ON products.category_id = category.id " + where
```

(`category` join stays — still needed for `Search` matching `category.name`.)

**Sort whitelist** — extend, keep it a strict allow-list (never interpolate raw user input):

```go
sortBy := "products.created_at"
switch params.SortBy {
case "title":
    sortBy = "products.title"
case "category_name":
    sortBy = "category.name"
case "price":
    sortBy = "products.price_min"
case "popularity":
    sortBy = "products.rating_count"
}

sortDir := "DESC"
if params.SortDir == "asc" {
    sortDir = "ASC"
}
```

Add a stable tiebreaker to every ORDER BY (important for pagination correctness — without it, rows with equal `price_min` or `rating_count` can shift between pages, or duplicate/skip across page boundaries):

```go
orderClause := fmt.Sprintf("%s %s, products.id ASC", sortBy, sortDir)
```

**Main query** — drop the LATERAL join, select denormalized columns directly:

```go
query := fmt.Sprintf(`
    SELECT
        products.id,
        products.handle,
        products.title,
        products.status,
        products.description,
        products.vendor,
        products.category_id,
        products.created_at,
        products.updated_at,
        category.slug AS category_slug,
        category.name AS category_name,
        products.price_min AS start_price,
        products.price_max AS max_price,
        products.rating_avg AS rating_avg,
        products.rating_count AS rating_count,
        thumbnail.type AS thumbnail_type,
        thumbnail.url AS thumbnail_url,
        thumbnail.alt_text AS thumbnail_alt_text
    FROM products
    LEFT JOIN category ON products.category_id = category.id
    LEFT JOIN LATERAL (
        SELECT pm.type, pm.url, pm.alt_text
        FROM product_media pm
        WHERE pm.product_id = products.id AND pm.position = 1
        LIMIT 1
    ) thumbnail ON true
    %s
    ORDER BY %s
    LIMIT $%d OFFSET $%d
`, where, orderClause, argIdx+1, argIdx+2)
```

(The `product_media` thumbnail LATERAL join stays — it's a 1-row-per-product lookup keyed on an indexable `position = 1`, not an aggregate, so it doesn't have the same scaling problem as the old price/rating joins.)

### 4.3 `productListRow` struct — add rating fields

```go
type productListRow struct {
	productRow
	CategorySlug     *string
	CategoryName     *string
	StartPrice       decimal.Decimal
	MaxPrice         decimal.Decimal
	RatingAvg        decimal.Decimal // NEW
	RatingCount      int             // NEW
	ThumbnailType    *string
	ThumbnailURL     *string
	ThumbnailAltText *string
}
```

Map into `ProductListItem` in `FindAll`'s result-building loop:

```go
item.Rating = domain.ProductRating{
    Average: row.RatingAvg,
    Count:   row.RatingCount,
}
```

---

## 5. HTTP handler / query-param parsing

- `rating` — comma-separated ints, e.g. `rating=3,4,5`. Parse, dedupe, and validate each value is in `1..5`; reject (400) on any out-of-range or non-numeric value rather than silently dropping it.
- `sort_by` — extend existing whitelist validation to accept `price`, `popularity` alongside `title`, `created_at`, `category_name`. Reject anything else with 400 (do not fall through silently to the default sort — that hides client bugs).
- `sort_dir` — unchanged (`asc`/`desc`, default as currently implemented).

No new endpoint needed — this is additive to the existing `GET /products` param set.

---

## 6. Testing checklist

- [ ] Backfill migration produces `price_min`/`price_max`/`rating_avg`/`rating_count` matching a live `JOIN`-computed baseline on the pre-migration dataset (write a one-off verification query, don't just trust it).
- [ ] Insert/update/delete a variant → `products.price_min`/`price_max` update correctly, including when a product's last non-deleted variant is soft-deleted (should fall back to `0`, not `NULL` or stale value).
- [ ] Insert/update/delete a `product_reviews` row → `rating_avg`/`rating_count` update correctly, including the last review being deleted (falls back to `0`/`0`).
- [ ] `rating=3,4,5` returns only products whose rounded `rating_avg` is in that set; products with `rating_count = 0` (avg 0) are correctly excluded unless `0` is explicitly requested (confirm whether `0`/no-reviews should ever be selectable — probably not, since `rating` values are 1-5 per `product_reviews.rating` constraint).
- [ ] `sort_by=price&sort_dir=asc/desc` matches manually computed cheapest/most expensive ordering.
- [ ] `sort_by=popularity&sort_dir=desc` matches `COUNT(product_reviews)` ordering.
- [ ] Pagination is stable across pages when many products share the same `price_min` or `rating_count` (verify the `products.id` tiebreaker prevents skip/duplicate rows).
- [ ] `EXPLAIN ANALYZE` on `sort_by=price` and `sort_by=popularity` queries under realistic filter combinations confirms the new indexes are used, not a full seq scan.
- [ ] Invalid `rating` values (e.g. `rating=0,7,abc`) return 400, not silently ignored.