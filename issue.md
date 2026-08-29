# Issue: Implement Product Reviews & Ratings API

Spec: [specs/reviews-api-spec.md](specs/reviews-api-spec.md)

## Scope decisions (read this before coding)

These decisions were made to keep this issue shippable without new external
service dependencies. Follow them unless you hit something that makes them
impossible — if so, stop and ask rather than improvising.

1. **No external service calls.** The spec references hydrating
   `user.display_name` from a "User/Auth Service" and `verified_only`
   filtering from purchase history. Neither exists in this repo.
   - `display_name` is **denormalized at write time**: capture the `name`
     claim from the JWT when a review is created (and re-save it on update,
     in case the name changed), and store it directly on the
     `product_reviews` row. No live hydration call is needed for listing.
   - `verified_only` query param: **accept it, but always treat it as a
     no-op** (do not filter) until purchase-history data exists. Document
     this in a code comment on the handler/use case, and mention it in the
     PR description.
   - Section 7's `product.name` / `thumbnail_url` hydration is **not**
     external — the `products` and gallery tables already live in this
     service. Join/query them directly from the reviews repo.
2. **`display_name` on the entity.** The spec's canonical Review JSON
   doesn't list `display_name`, but the proposed DBML schema stores it. Keep
   it as a domain field on `Review`, populated from the JWT at write time.
   Do not add a separate hydration path for it.
3. **No admin/moderation override.** The JWT payload has no role claim, so
   section 6 (Delete) and section 5 (Update) enforce **author-only** access.
   Do not build moderation/admin-delete — that's explicitly called out in
   the spec's "Notes" section as a future extension.
4. **JWT library:** use `github.com/golang-jwt/jwt/v5`.
5. **IDs:** per `CLAUDE.md` convention, generate the review `id` as a UUIDv7
   in the use case layer (`github.com/google/uuid`), not via Postgres
   `gen_random_uuid()`. Adjust the DBML default accordingly when writing the
   migration (see Step 1).
6. **`average_rating`:** use `github.com/shopspring/decimal`, not `float64`,
   per repo convention.

If anything else in the spec is ambiguous once you're implementing, stop and
ask instead of guessing.

---

## Step 1 — Migration

Create `migrations/0003_create_product_reviews.sql` based on the proposed
DBML, with these adjustments:

- Drop the `default: gen_random_uuid()` on `id` — the app supplies a UUIDv7.
- `user_id` and `display_name` have no FK (no local `users` table); leave
  them as plain `uuid` / `text` columns with `not null`.
  - Note: the DBML in the spec request has `display_name uuid` — this is a
    copy-paste bug from `user_id`. It must be `text`, not `uuid`.
- Add `rating` check constraint: `CHECK (rating BETWEEN 1 AND 5)`.
- Keep the two indexes as specified:
  - `idx_reviews_product_date` on `(product_id, created_at)`
  - `uq_user_product_review` unique on `(user_id, product_id)`
- FKs: `product_id` → `products.id` `ON DELETE CASCADE`,
  `variant_id` → `variants.id` `ON DELETE SET NULL` (nullable column).
- Add a trigger or rely on application code to bump `updated_at` — check
  how existing tables in this repo handle `updated_at` (e.g. `products`)
  and follow the same pattern for consistency.

Verify column names/types against the **actual** `products` and `variants`
tables (check existing migrations / `internal/domain/product.go` and
`variant.go`) before assuming the FK types match — don't just trust the
spec's uuid assumption blindly.

---

## Step 2 — Domain layer (`internal/domain/review.go`)

Define, in one new file:

- `Review` struct with `db:"..."` tags matching the migration:
  `ID, ProductID, VariantID (*uuid.UUID), UserID, DisplayName, Rating, Title (*string), Comment (*string), CreatedAt, UpdatedAt`.
- `ReviewFilter` struct for list query params: `Page, Limit, Rating *int, HasComment *bool, Sort string` (`verified_only` accepted but not filtered — see scope decision #1).
- `RatingSummary` struct: `ProductID uuid.UUID`, `AverageRating decimal.Decimal`, `TotalReviews int`, `RatingBreakdown map[int]int`.
- Repository interface `ReviewRepository` with methods your use cases will need, e.g.:
  - `Insert(ctx, review *Review) error`
  - `FindByID(ctx, id uuid.UUID) (*Review, error)`
  - `FindByProduct(ctx, productID uuid.UUID, filter ReviewFilter) ([]Review, int, error)` (int = total count for pagination)
  - `FindByUser(ctx, userID uuid.UUID, page, limit int) ([]Review, int, error)` — should join `products` (+ gallery) for section 7's hydrated `product` object; consider whether this belongs on `ReviewRepository` or needs a small joined struct (`ReviewWithProduct`) — decide and document.
  - `Update(ctx, review *Review) error`
  - `Delete(ctx, id uuid.UUID) error`
  - `GetSummary(ctx, productID uuid.UUID) (*RatingSummary, error)`
  - `VariantBelongsToProduct(ctx, variantID, productID uuid.UUID) (bool, error)` — used for the 404 validation rule on create.
- Use case interfaces (mirror the `category` module's split style —
  `QueryReviewUseCase`, `InsertReviewUseCase`, `UpdateReviewUseCase`,
  `DeleteReviewUseCase`, or combine if that's cleaner — match whatever
  granularity the `product` module already uses for its use cases, since
  reviews has a similar CRUD+list+summary shape).

---

## Step 3 — JWT Auth middleware

Create `internal/delivery/http/middleware/auth.go`.

- Add `API_JWT_SECRET` to `.env.example` and read it via `os.Getenv` (there
  is no central config package yet — follow the existing pattern of reading
  env vars where needed, e.g. as done for `APP_ENV` in `cmd/main.go`).
- Middleware function `RequireAuth(secret string) gin.HandlerFunc`:
  1. Read `Authorization` header, expect `Bearer <token>`; if missing/malformed → `401`.
  2. Parse and verify the JWT using `golang-jwt/jwt/v5` with `HS256` and the secret. Reject on `iat`/`exp` failure or bad signature → `401`.
  3. Extract `sub` claim as the user ID; parse as `uuid.UUID` → `401` if invalid.
  4. Extract `name` and `email` claims.
  5. Store `user_id`, `name`, `email` in the Gin context (`c.Set(...)`) using constants (e.g. `middleware.ContextUserIDKey`) so handlers can retrieve them without magic strings.
  6. Call `c.Next()`.
- Use the **standard error shape** from the spec (`{"error": {"code": ..., "message": ..., "details": {}}}`) for the 401 response body, with a code like `UNAUTHENTICATED`.
- Write a helper in the same package, e.g. `GetUserID(c *gin.Context) (uuid.UUID, bool)`, `GetUserName(c *gin.Context) (string, bool)`, for handlers to use.

Add `github.com/golang-jwt/jwt/v5` to `go.mod` via `go get`.

---

## Step 4 — Repository (`internal/infrastructure/repository/reviewRepo.go`)

Follow the `category`/`product` repo conventions:
- Use `pgx.CollectRows` + `pgx.RowToStructByName`.
- Wrap every method with `metrics.ObserveDB("review", "<operation>")(time.Now())`.
- Pagination: `LIMIT`/`OFFSET` plus a `COUNT(*)` query (or a window function) for `total_items`.
- Sorting: implement `newest` (default, uses `idx_reviews_product_date` DESC), `oldest` (same index ASC), `highest_rating`, `lowest_rating` — whitelist the sort param against a fixed map, never interpolate raw user input into `ORDER BY`.
- `GetSummary`: aggregate query — `COUNT(*)`, `AVG(rating)`, and a `GROUP BY rating` count for the breakdown (1–5, fill in zeros for missing ratings).
- `FindByUser`: join `products` (and whatever gallery table/position-1 lookup exists — check `internal/domain/product.go` / media tables) to populate the "My Reviews" product summary.
- Enforce the `uq_user_product_review` conflict as a Postgres `23505` error mapped to a domain-level `ErrReviewAlreadyExists` sentinel error, so the use case/handler can map it to `409`.

---

## Step 5 — Use cases (`internal/app/review/`)

One file per operation, matching the `category` module's layout
(`insertUseCase.go`, `queryUseCase.go`, `updateUseCase.go`, `deleteUseCase.go`, plus a `summaryUseCase.go`):

- **Insert**: generate UUIDv7 id, validate `rating` 1–5 and string lengths (title ≤150, comment ≤5000) — validator tags on the DTO are fine for this, business-level checks (variant belongs to product, product exists) belong here. Set `DisplayName` from the JWT `name` claim passed in from the handler. Map `uq_user_product_review` conflict → `409`.
- **Query (list by product)**: apply filter/pagination/sort, compute `total_pages`.
- **Query (single)**: 404 if not found.
- **Query (by user)**: for section 7.
- **Update**: load review, verify `review.UserID == callerUserID` → else `403`; apply provided fields, bump `updated_at`.
- **Delete**: same ownership check → `403`.
- **Summary**: wraps `GetSummary`.

Constructors return domain interface types, not concrete structs (per `CLAUDE.md`).

---

## Step 6 — DTOs (`internal/delivery/http/dto/review.go`)

Mirror the JSON shapes exactly as specified in
[specs/reviews-api-spec.md](specs/reviews-api-spec.md):
- `CreateReviewRequest`, `UpdateReviewRequest` (validator tags: `required`, `min=1,max=5` for rating, `max=150`/`max=5000` for strings).
- `ReviewResponse` (section 1 / "Common Object Reference" shape).
- `ReviewListItemResponse` (section 2 shape, with nested `user{id,display_name}`).
- `RatingSummaryResponse` (section 3).
- `UserReviewListItemResponse` (section 7, nested `product{id,name,thumbnail_url}`).
- `PaginationResponse{page,limit,total_items,total_pages}`.
- `ErrorResponse` matching the "Standard Error Shape".

Add mapping functions from domain → DTO (`ToReviewResponse(domain.Review) ReviewResponse`, etc.) in this file or a sibling `mapper.go`, following whatever the `product` module already does for its DTOs.

---

## Step 7 — Handler (`internal/delivery/http/handler/reviewHandler.go`)

`ReviewHandler` struct holding the use cases, constructor `NewReviewHandler(...)`. One method per endpoint:

| Method | Route | Notes |
|---|---|---|
| `Create` | `POST /products/:id/reviews` | needs auth |
| `Fetch` | `GET /products/:id/reviews` | public |
| `Summary` | `GET /products/:id/reviews/summary` | public |
| `GetByID` | `GET /reviews/:reviewId` | public |
| `Update` | `PATCH /reviews/:reviewId` | needs auth + ownership |
| `Delete` | `DELETE /reviews/:reviewId` | needs auth + ownership |
| `FetchMine` | `GET /users/me/reviews` | needs auth |

- Pull `user_id`/`name` out of the Gin context via the middleware helpers from Step 3 — never trust a body-supplied `user_id`.
- Map domain errors to HTTP statuses per the spec's error tables (400/401/403/404/409).
- Add Swagger annotations (`@Summary`, `@Router`, etc.) following the existing handlers' style so `swag init` picks them up.

---

## Step 8 — Router (`internal/delivery/http/router.go`)

Add to `SetupRouter`'s signature: `reviewHandler *handler.ReviewHandler` and the JWT secret (or a pre-built `gin.HandlerFunc` for auth — decide based on how `gin_server.go` constructs things).

Register routes, applying `middleware.RequireAuth(secret)` only to the write endpoints:

```go
products.GET("/:id/reviews", reviewHandler.Fetch)
products.GET("/:id/reviews/summary", reviewHandler.Summary)
products.POST("/:id/reviews", middleware.RequireAuth(secret), reviewHandler.Create)

reviews := v1.Group("/reviews")
{
    reviews.GET("/:reviewId", reviewHandler.GetByID)
    reviews.PATCH("/:reviewId", middleware.RequireAuth(secret), reviewHandler.Update)
    reviews.DELETE("/:reviewId", middleware.RequireAuth(secret), reviewHandler.Delete)
}

users := v1.Group("/users")
{
    users.GET("/me/reviews", middleware.RequireAuth(secret), reviewHandler.FetchMine)
}
```

Watch for route conflicts with the existing `products.GET("/:id", ...)` — Gin's router should be fine here since `/reviews` is a distinct static segment after `:id`, but double check with existing tests in `router_routes_test.go`.

---

## Step 9 — Wire container (`internal/wire/container.go`)

Add the `review` module wiring, same pattern as `category`/`product`:

```go
reviewRepo := repository.NewReviewRepo(db)
reviewInsertUC := review.NewReviewInsertUseCase(reviewRepo)
reviewQueryUC := review.NewReviewQueryUseCase(reviewRepo)
reviewUpdateUC := review.NewReviewUpdateUseCase(reviewRepo)
reviewDeleteUC := review.NewReviewDeleteUseCase(reviewRepo)
reviewSummaryUC := review.NewReviewSummaryUseCase(reviewRepo)
reviewHandler := handler.NewReviewHandler(reviewInsertUC, reviewQueryUC, reviewUpdateUC, reviewDeleteUC, reviewSummaryUC)
```

Add `ReviewHandler` to the `Container` struct. Thread the JWT secret through `NewContainer` or read it separately in `cmd/server/gin_server.go` — check how `gin_server.go` currently builds `SetupRouter`'s args and follow the same wiring style.

---

## Step 10 — Tests

- Unit tests for each use case (mirror `internal/app/infrachecker`'s test style: fake/mock the repository interface).
- Middleware test: valid token → context populated; expired token → 401; malformed header → 401; bad signature → 401.
- At minimum, cover: create success, duplicate review conflict (409), update by non-owner (403), variant not belonging to product (404).

---

## Step 11 — Docs

Run `swag init -g cmd/main.go -o docs --parseInternal` from the repo root after adding handler annotations, and commit the regenerated `docs/` output.

---

## Out of scope (explicitly, per spec's own "Notes" section)

Do not build: helpful votes, moderation/reporting workflow, seller responses, review media/images, or verified-purchase filtering. These need schema additions not covered by this issue.
