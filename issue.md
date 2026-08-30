# Issue: Implement Rating Filter + Price/Popularity Sort on `GET /products`

Spec: [specs/product-filter-rating-price-sort.md](specs/product-filter-rating-price-sort.md)

This document is a step-by-step implementation guide for whoever picks this up
(junior engineer or another LLM agent). It has been checked against the
**actual current code** (not just the spec) — a few things in the spec don't
match this repo's real conventions verbatim; the deltas are called out
explicitly in each step so you don't copy-paste something that breaks the
build or is inconsistent with the rest of the codebase.

## Before you start — deltas from the spec you must apply

1. **JSON casing.** The spec's Go snippets use `snake_case` json tags
   (`start_price`, `rating_avg`...). This repo's actual read-model
   (`internal/domain/product_readmodel.go`) uses `camelCase`
   (`startPrice`, `maxPrice`, `altText`). The new `ProductRating` struct must
   follow the existing convention: `json:"average"` / `json:"count"`, not
   snake_case.
2. **`price_stats` still exists today.** The spec describes it as if it's
   about to be removed. It's real, in
   `internal/infrastructure/repository/productRepo.go` (`priceStatsJoin`
   const, ~line 214), and `buildListConditions` currently references
   `price_stats.max_price` / `price_stats.min_price`. There are also existing
   unit tests in `productRepo_test.go` that assert on those exact strings —
   you must update those tests as part of this change (see Step 6), or the
   build will pass but tests will fail.
3. **Existing `sort_by` whitelist is duplicated in two places** with two
   different styles: the handler (`product_handler.go`) validates with an
   `if/else` chain and rejects with `gin.H{"error": ...}`, while
   `productRepo.go`'s `FindAll` has its own `if/else` mapping column names.
   Both need the two new values added, consistently.
4. **Price query params are `minPrice`/`maxPrice` (camelCase), not
   `min_price`/`max_price`.** Follow that convention for any doc/swagger
   comments; it doesn't affect the new `rating` param (which has no casing
   ambiguity), but don't introduce `snake_case` for anything else.
5. **Migrations are plain numbered `.sql` files** (`migrations/0001_...sql`
   through `0003_...sql`), not a `migrate`/`goose` tool config. There's no
   migration runner detected in the repo — check with whoever runs
   deployments how these are applied (psql manually / CI step) before
   assuming `0004` is auto-picked-up.
6. **A separate, unrelated rating aggregate already exists.** `GET
   /products/:id/reviews/summary` (`internal/app/review/summaryUseCase.go` +
   `reviewRepo.go` `GetSummary`) computes `COUNT`/`AVG` live, per single
   product, on demand. That is fine to leave as-is — it's a different use
   case (single-product detail vs. paginated list filter/sort) and low
   volume. Do **not** try to merge it with the new denormalized
   `products.rating_avg`/`rating_count` columns in this task; that's a
   separate, later optimization if it's ever needed.
7. **Array parameter type.** For the `rating IN (...)` filter, don't bind a
   raw Go `[]int` as the pgx query argument — Go's `int` is 64-bit and pgx
   will infer `int8[]`, which won't match cleanly against `integer` columns.
   Convert to `[]int32` explicitly before passing it as a query arg.

---

## Step 1 — Migration: add denormalized columns + backfill + triggers + indexes

Create `migrations/0004_add_product_price_rating_stats.sql`. Combine columns,
backfill, triggers, and indexes into this single file (in that order — order
matters, see spec §2.2's note about backfilling before triggers see live
traffic). Use the SQL exactly as written in spec §2.1–§2.4:

- `ALTER TABLE products ADD COLUMN price_min ... price_max ... rating_avg ...
  rating_count ...` (spec §2.1)
- Backfill `UPDATE ... FROM` for both price and rating (spec §2.2)
- `refresh_product_price_stats` / `trg_variants_price_stats` /
  `variants_price_stats_sync` trigger (spec §2.3)
- `refresh_product_rating_stats` / `trg_reviews_rating_stats` /
  `product_reviews_rating_stats_sync` trigger (spec §2.3)
- The three indexes in spec §2.4

Apply it against your local dev DB and verify no errors before moving on:

```bash
psql "$DATABASE_URL" -f migrations/0004_add_product_price_rating_stats.sql
```

**Verify the backfill**, don't just trust it — run this and confirm it
returns zero rows (spec's testing checklist item 1):

```sql
SELECT p.id, p.price_min, p.price_max, v.min_price, v.max_price
FROM products p
JOIN (
  SELECT product_id, MIN(price) min_price, MAX(price) max_price
  FROM variants WHERE is_deleted = false GROUP BY product_id
) v ON v.product_id = p.id
WHERE p.price_min <> v.min_price OR p.price_max <> v.max_price;
```

Do the equivalent for `rating_avg`/`rating_count` against
`product_reviews`.

**Manually exercise the triggers** before trusting them in code:
- Insert a variant on an existing product → confirm `products.price_min`/
  `price_max` update.
- Soft-delete (`is_deleted = true`) the last remaining variant of a product →
  confirm it falls back to `0`, not `NULL`.
- Insert/delete a `product_reviews` row → confirm `rating_avg`/`rating_count`
  update, and that deleting the last review resets both to `0`.

## Step 2 — Domain changes (`internal/domain/product_readmodel.go`)

Add the `ProductRating` struct (camelCase tags, per delta #1 above):

```go
type ProductRating struct {
	Average decimal.Decimal `json:"average"`
	Count   int             `json:"count"`
}
```

Add `Rating` to `ProductListItem`:

```go
type ProductListItem struct {
	Product
	Category  ProductCategory   `json:"category"`
	Prices    ProductPrices     `json:"prices"`
	Rating    ProductRating     `json:"rating"`
	Thumbnail *ProductThumbnail `json:"thumbnail"`
}
```

Extend `ListProductParams`:

```go
type ListProductParams struct {
	Search     string
	CategoryID int
	Status     string
	MinPrice   *decimal.Decimal
	MaxPrice   *decimal.Decimal
	Ratings    []int  // optional; empty/nil means "no filter". Set-membership on ROUND(rating_avg). Values 1-5, validated at handler layer.
	Page       int
	PerPage    int
	SortBy     string // whitelisted: "title", "created_at", "category_name", "price", "popularity"
	SortDir    string // "asc" | "desc"
}
```

Update the doc comment on `SortBy` in the struct to list the two new values.

## Step 3 — Repository: `buildListConditions` (`internal/infrastructure/repository/productRepo.go`)

Replace the two existing price conditions (currently referencing
`price_stats.max_price` / `price_stats.min_price`) with the denormalized
columns:

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

Add the rating filter (note the `[]int32` conversion per delta #7):

```go
if len(params.Ratings) > 0 {
    argIdx++
    ratings32 := make([]int32, len(params.Ratings))
    for i, r := range params.Ratings {
        ratings32[i] = int32(r)
    }
    args = append(args, ratings32)
    conditions = append(conditions, fmt.Sprintf("ROUND(products.rating_avg) = ANY($%d)", argIdx))
}
```

## Step 4 — Repository: `FindAll` (same file)

1. **Delete** the `priceStatsJoin` const entirely — it's no longer needed
   once `products.price_min`/`price_max` are read directly.
2. **Count query**: remove `priceStatsJoin` from the concatenation —
   `countQuery := "SELECT COUNT(*) FROM products LEFT JOIN category ON products.category_id = category.id " + where`.
3. **Sort whitelist**: extend the existing `if/else` (or convert to
   `switch`, either is fine, just stay consistent) to add `price` →
   `products.price_min` and `popularity` → `products.rating_count`.
4. **Add a stable tiebreaker** to the ORDER BY — this is required for
   pagination correctness whenever many rows share the same `price_min` or
   `rating_count`:

   ```go
   orderClause := fmt.Sprintf("%s %s, products.id ASC", sortBy, sortDir)
   ```

   and use `ORDER BY %s` with `orderClause` instead of the current
   `ORDER BY %s %s` two-arg form.
5. **Main query**: remove the `priceStatsJoin` from the `FROM` clause (and
   its `%s` placeholder), read `products.price_min`/`price_max` directly
   instead of `COALESCE(price_stats.min_price, 0)` (they're already
   `NOT NULL DEFAULT 0` post-migration, so no `COALESCE` needed), and add
   `products.rating_avg AS rating_avg, products.rating_count AS
   rating_count` to the SELECT list.
6. **`productListRow` struct** (~line 1095): add

   ```go
   RatingAvg   decimal.Decimal `db:"rating_avg"`
   RatingCount int             `db:"rating_count"`
   ```
7. **Result-building loop**: map the new fields —

   ```go
   item.Rating = domain.ProductRating{
       Average: row.RatingAvg,
       Count:   row.RatingCount,
   }
   ```

## Step 5 — HTTP handler (`internal/delivery/http/handler/product_handler.go`)

1. **Extend `sort_by` validation** (~line 139) to accept `price` and
   `popularity`. Update the Swagger `@Param sort_by` comment (~line 115) and
   the error message text to list all five values.
2. **Add `rating` query param parsing.** Follow the existing
   `parsePriceQueryParam` pattern (a small helper, not inline logic) — write
   a `parseRatingsQueryParam(c *gin.Context) ([]int, error)`:
   - Read `c.Query("rating")`. Empty string → return `nil, nil` (no filter).
   - Split on `,`, trim whitespace.
   - Parse each as int; if any fails to parse, or is outside `1..5`, return
     an error — **reject the whole request with 400**, don't silently drop
     bad values (spec §5 / testing checklist item 8).
   - Dedupe values before returning (a `map[int]struct{}` pass is enough).
3. Wire it into `params := domain.ListProductParams{...}` as `Ratings:
   ratings`.
4. Use `errorResponse("ERR_VALIDATION", ...)` for the new rating validation
   error, matching the pattern already used for the `minPrice`/`maxPrice`
   validation just above it in the same handler (more consistent than the
   older bare `gin.H{"error": ...}` used by the pre-existing `sort_by`/
   `sort_dir` checks — you don't need to refactor those, just don't copy
   their older style for new code).
5. Add a `@Param rating query string false "Comma-separated list of rounded average ratings to filter by, e.g. rating=3,4,5. Each value must be 1-5."`
   Swagger annotation.

## Step 6 — Update existing tests that hardcode `price_stats`

`internal/infrastructure/repository/productRepo_test.go` currently asserts
generated conditions equal strings like `"price_stats.max_price >= $1"` (see
lines ~74, 88, 104-105, 122, 163-164) and asserts `priceStatsJoin` contains
`price_stats`/`min_price`/`max_price` (~line 175-179). Since Step 3 changes
the generated SQL to reference `products.price_max`/`products.price_min`
and Step 4 deletes `priceStatsJoin` entirely:

- Update every expected condition string from `price_stats.max_price >= $N`
  → `products.price_max >= $N`, and `price_stats.min_price <= $N` →
  `products.price_min <= $N`.
- Delete the test (or sub-assertion) that checks `priceStatsJoin`'s contents,
  since the constant no longer exists.
- Add new test cases for the `Ratings` filter in `buildListConditions`
  (single rating, multiple ratings, no ratings → no condition), following
  the existing `assertConditionsArgs` helper pattern in the same file.

## Step 7 — Write new tests

Cover at minimum (mirroring spec §6's checklist, scoped to what's
unit-testable without a live DB — the DB-level checks like trigger behavior
and `EXPLAIN ANALYZE` need to be run manually against a real Postgres, not as
Go unit tests):

- `buildListConditions` with `Ratings` set (Step 6, above).
- Handler-level: `rating=3,4,5` parses correctly; `rating=0,7,abc` returns
  400; `rating=` (empty) is treated as "no filter", not an error.
- Handler-level: `sort_by=price` and `sort_by=popularity` are accepted;
  something outside the whitelist (e.g. `sort_by=foo`) still returns 400.
- `productRepo_test.go`: extend the existing `FindAll`-adjacent tests (if
  any hit a real/test DB) to check `ORDER BY ..., products.id ASC` is
  present in generated queries, if there's a query-string-level test — if
  `FindAll` is only tested via `buildListConditions` unit tests today (which
  don't build the ORDER BY), you can skip this and rely on manual
  verification instead.

## Step 8 — Manual DB verification (not automatable as Go tests)

Run these against a dev DB seeded with realistic-ish data before calling
this done — this is what spec §6 items 4, 5, 6, 7 are checking for:

- `rating=3,4,5` returns only products whose `ROUND(rating_avg)` is 3, 4, or
  5; confirm products with zero reviews (`rating_avg = 0`) are excluded.
- `sort_by=price&sort_dir=asc` and `desc` match manual cheapest/most
  expensive ordering.
- `sort_by=popularity&sort_dir=desc` matches `COUNT(product_reviews)`
  ordering (i.e. matches `rating_count`).
- Paginate through a filtered result set where several products share the
  same `price_min` or `rating_count` — confirm no duplicate or skipped rows
  across pages (this is what the `products.id ASC` tiebreaker from Step 4
  is for).
- `EXPLAIN ANALYZE` the list query under `sort_by=price` and
  `sort_by=popularity` with `status=active` — confirm it uses
  `idx_products_status_price_min` / `idx_products_status_rating_cnt`
  respectively, not a sequential scan.

## Step 9 — Regenerate Swagger docs

From the repo root, after the handler annotations are updated:

```bash
swag init -g cmd/main.go -o docs --parseInternal
```

## Step 10 — Full test suite + build

```bash
go build ./...
go test ./...
```

Both must pass clean before opening a PR.

---

## Open question to flag back to the requester before merging (not before implementing)

Spec §3 flags this and it's worth restating: `Ratings` is implemented as an
**exact rounded-value match** (`rating=3,4,5` → `ROUND(rating_avg) IN
(3,4,5)`), not an "X stars & up" threshold. If the actual product/UX intent
is "3 stars & up", the filter condition changes to `products.rating_avg >=
$min` and the query param semantics change entirely (single value, not a
list). Implement the spec as written (exact match), but call this out
explicitly in the PR description so a reviewer with product context can
catch it if the assumption is wrong — don't guess silently either way.
