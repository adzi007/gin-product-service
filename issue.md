# Implement Variant and Media/Gallery Management Endpoints

## Context

The `product` module currently supports product CRUD, lifecycle (archive/restore/purge), and options/option-values management (`internal/app/product/optionUseCase.go`, `internal/domain/product.go` → `OptionUseCase`, routes in `internal/delivery/http/router.go`). Variants and product media are currently only created inline as part of `POST /api/v1/products` (see `CreateProductInput.Variants` / `CreateProductInput.Gallery` in `internal/domain/product.go`), and read back via `domain.Product.Variants` / `domain.Product.Media`. There is **no standalone CRUD** for variants or media yet — this issue adds it.

The `domain.Variant`, `domain.VariantMedia`, and `domain.ProductMedia` structs already exist in [internal/domain/product.go](internal/domain/product.go) — reuse them, don't redefine.

Follow the exact layering already used by the options feature as your template:
- Domain contracts + input/output structs → `internal/domain/product.go`
- Business logic → new files in `internal/app/product/` (one file per concern, e.g. `variantUseCase.go`, `mediaUseCase.go`)
- Postgres queries → `internal/infrastructure/repository/productRepo.go` (add methods to the existing `ProductRepository` impl — do not create a new repo file)
- HTTP handlers → `internal/delivery/http/handler/product_handler.go` (add methods to the existing `ProductHandler` — do not create a new handler file)
- Routes → `internal/delivery/http/router.go`, inside the existing `products := v1.Group("/products")` block plus a new top-level `variants := v1.Group("/variants")` group (since several endpoints hang off `/v1/variants/:id` directly, not nested under a product)
- Wiring → `internal/wire/container.go` (no new repo/use case objects needed if you add methods to existing `ProductRepository`/handler; only touch this file if you introduce a new use case type, e.g. `VariantUseCase`, `MediaUseCase`)

Read `internal/app/product/optionUseCase.go` and `internal/domain/product.go` (interfaces `OptionUseCase`, `ProductRepository`, and structs `CreateOptionInput`/`ReorderOptionsInput`/`PositionUpdate`) before writing any code — the option feature is a near-exact structural precedent for variants (an ID-scoped child resource with position/reorder semantics) and for media (attach/detach + reorder).

## Endpoints to implement

### Variants
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/products/:id/variants` | Create one variant |
| `POST` | `/v1/products/:id/variants/bulk` | Bulk create variants (import/sync) |
| `PATCH` | `/v1/variants/:id` | Update variant fields (price, sku, barcode, title, weight, etc.) |
| `PATCH` | `/v1/products/:id/variants/bulk` | Bulk update variants, array of `{id, fields}` |
| `DELETE` | `/v1/variants/:id` | Delete variant (soft if has history, hard otherwise) |
| `POST` | `/v1/products/:id/variants/bulk-delete` | Bulk delete variants |
| `POST` | `/v1/variants/:id/restore` | Undo a soft delete |
| `PATCH` | `/v1/products/:id/variants/reorder` | Bulk position update for variants |

### Media / Gallery
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/products/:id/media` | Upload/attach new media to product gallery (accepts array) |
| `PATCH` | `/v1/products/:id/media/:media_id` | Update media metadata (alt_text, etc.) |
| `DELETE` | `/v1/products/:id/media/:media_id` | Remove media from product entirely (cascades variant_media) |
| `PATCH` | `/v1/products/:id/media/reorder` | Bulk position update for product gallery |
| `POST` | `/v1/variants/:id/media` | Attach existing product media to a variant |
| `DELETE` | `/v1/variants/:id/media/:media_id` | Detach media from variant only (media stays in product) |
| `PATCH` | `/v1/variants/:id/media/reorder` | Bulk position update within a variant's media subset |

## Step-by-step plan

Work top-down: domain → repository → use case → handler → router → wire → tests. Do variants first, then media, since media's variant-attach endpoints depend on variants existing.

### 1. Domain layer (`internal/domain/product.go`)

1. Add request/response structs next to the existing `VariantInput`/`VariantMediaInput` (around line 320+):
   - `CreateVariantInput` (mirrors `VariantInput` minus `ProductID`, since that comes from the URL param)
   - `BulkCreateVariantsInput { Variants []VariantInput }`
   - `UpdateVariantInput` — all fields pointers/optional (`SKU *string`, `Barcode *string`, `Title *string`, `Price *decimal.Decimal`, `Weight *decimal.Decimal`), so a `PATCH` can update a subset
   - `BulkUpdateVariantsInput { Updates []VariantUpdateItem }` where `VariantUpdateItem { ID uuid.UUID; Fields UpdateVariantInput }`
   - `BulkDeleteVariantsInput { IDs []uuid.UUID }`
   - Reuse `ReorderOptionsInput`/`PositionUpdate` shape for variant reorder — either reuse the existing `PositionUpdate` struct directly or add a `ReorderVariantsInput { Positions []PositionUpdate }` alias-style struct for clarity.
   - `CreateMediaInput` / `BulkCreateMediaInput` for `POST /products/:id/media` (fields: `Type string`, `URL string`, `AltText *string`) — check if `GalleryMediaInput` (referenced in `CreateProductInput.Gallery`) already covers this shape before adding a new one; reuse it if so.
   - `UpdateMediaInput { AltText *string }`
   - `AttachVariantMediaInput { MediaID uuid.UUID }` (for attaching existing product media to a variant)
   - `ReorderMediaInput { Positions []PositionUpdate }` (reused for both product-gallery reorder and variant-media-subset reorder)

2. Add two new use case interfaces (or extend `OptionUseCase`-style single interface per resource — prefer separate interfaces, consistent with `InsertProductUseCase`/`QueryProductUseCase`/`UpdateProductUseCase` being split by concern):
   ```go
   type VariantUseCase interface {
       Create(ctx context.Context, productID uuid.UUID, input CreateVariantInput) (Variant, error)
       BulkCreate(ctx context.Context, productID uuid.UUID, input BulkCreateVariantsInput) ([]Variant, error)
       Update(ctx context.Context, variantID uuid.UUID, input UpdateVariantInput) (Variant, error)
       BulkUpdate(ctx context.Context, productID uuid.UUID, input BulkUpdateVariantsInput) ([]Variant, error)
       Delete(ctx context.Context, variantID uuid.UUID) error
       BulkDelete(ctx context.Context, productID uuid.UUID, input BulkDeleteVariantsInput) error
       Restore(ctx context.Context, variantID uuid.UUID) (Variant, error)
       Reorder(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
   }

   type MediaUseCase interface {
       Create(ctx context.Context, productID uuid.UUID, input BulkCreateMediaInput) ([]ProductMedia, error)
       Update(ctx context.Context, productID, mediaID uuid.UUID, input UpdateMediaInput) (ProductMedia, error)
       Delete(ctx context.Context, productID, mediaID uuid.UUID) error
       Reorder(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
       AttachToVariant(ctx context.Context, variantID uuid.UUID, input AttachVariantMediaInput) (VariantMedia, error)
       DetachFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) error
       ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []PositionUpdate) error
   }
   ```

3. Extend `ProductRepository` with the persistence methods backing the above (see step 2 for exact SQL responsibilities). Add:
   ```go
   CreateVariant(ctx context.Context, variant Variant) (Variant, error)
   CreateVariants(ctx context.Context, variants []Variant) ([]Variant, error)
   FindVariantByID(ctx context.Context, variantID uuid.UUID) (Variant, error)
   UpdateVariant(ctx context.Context, variantID uuid.UUID, input UpdateVariantInput) (Variant, error)
   DeleteVariant(ctx context.Context, variantID uuid.UUID, hard bool) error
   BulkDeleteVariants(ctx context.Context, variantIDs []uuid.UUID, hard bool) error
   RestoreVariant(ctx context.Context, variantID uuid.UUID) (Variant, error)
   ReorderVariants(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
   VariantHasHistory(ctx context.Context, variantID uuid.UUID) (bool, error) // decides soft vs hard delete

   CreateProductMedia(ctx context.Context, media []ProductMedia) ([]ProductMedia, error)
   UpdateProductMedia(ctx context.Context, mediaID uuid.UUID, altText *string) (ProductMedia, error)
   DeleteProductMedia(ctx context.Context, mediaID uuid.UUID) error // must cascade-delete variant_media rows
   ReorderProductMedia(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
   FindMediaByID(ctx context.Context, mediaID uuid.UUID) (ProductMedia, error)

   AttachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) (VariantMedia, error)
   DetachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) error
   ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []PositionUpdate) error
   ```

### 2. Repository layer (`internal/infrastructure/repository/productRepo.go`)

1. Implement each new `ProductRepository` method using `pgx/v5`, following the exact conventions already used for `CreateOption`/`RenameOption`/`DeleteOption`/`ReorderOptions` in this file (same file, search for those method names to see the pattern: `pgx.CollectRows` + `pgx.RowToStructByName`, `metrics.ObserveDB("product", "<operation_name>")(time.Now())` wrapping every method).
2. `CreateVariants` (bulk) should use a single multi-row `INSERT ... VALUES (...), (...)` or a batched `pgx.Batch` — do not loop with N individual round trips.
3. `DeleteVariant`/`BulkDeleteVariants`: soft-delete means `UPDATE variants SET is_deleted = true`; hard delete means `DELETE FROM variants WHERE id = ...`. `VariantHasHistory` should check for existing rows referencing the variant (e.g. `stock_moves`, `order_line_items` — check the actual schema/migrations for what tables reference `variant_id`; if no such table exists yet in migrations, hard-delete unconditionally and leave a comment noting the history check is a stub until stock movement is implemented).
4. `DeleteProductMedia` must cascade to `variant_media` in the **same transaction** — wrap in `pgx.Tx` per the CLAUDE.md rule: "Multi-table writes ... must be wrapped in a DB transaction at the repository layer." Delete from `variant_media WHERE media_id = $1` then `product_media WHERE id = $1`, commit both or neither.
5. Reorder methods: use the same bulk `UPDATE ... FROM (VALUES ...) AS v(id, position) WHERE table.id = v.id` pattern as `ReorderOptions` — read that method's SQL directly and copy its shape for `variants`, `product_media`, and `variant_media` (scoped by `variant_id` in the last case).
6. Check for existing SQL migration files (`ls` any `migrations/` directory) — if the `variants`, `product_media`, `variant_media` tables don't already exist per the PRD schema, add migrations before writing queries against them. Confirm table/column names against migrations, not just the PRD, since PRD is described in CLAUDE.md as target-not-current-state.

### 3. Use case layer (`internal/app/product/variantUseCase.go`, `internal/app/product/mediaUseCase.go`)

1. Mirror `optionUseCase.go` structure: a private struct holding `productRepo domain.ProductRepository`, a `New...UseCase(productRepo) domain.VariantUseCase` / `domain.MediaUseCase` constructor, one method per interface method.
2. Every ID (`Variant.ID`, `ProductMedia.ID`) must be generated with `uuid.Must(uuid.NewV7())` in the use case layer, never left to a DB default (per CLAUDE.md convention).
3. `Create`/`BulkCreate` for variants: look up the product first via `productRepo.FindByID` to confirm it exists and to compute `Position` as `len(product.Variants)` (or the next value for bulk), exactly like `optionUc.Create` does for options.
4. Delete: call `VariantHasHistory` first to decide `hard` bool passed to `DeleteVariant`.
5. Wrap every operation with `logger.L(ctx).Error(...)` on failure and an `Info` log on success, matching the exact message/field style in `optionUseCase.go` (e.g. `"[INFO] Success create option"` → use `"[INFO] Success create variant"`, etc.) for log consistency across the module.
6. For `MediaUseCase.AttachToVariant`, validate the media being attached actually belongs to the variant's parent product (fetch `FindMediaByID`, compare `ProductID` against the variant's `ProductID` obtained via `FindVariantByID`) — this enforces the "attach *existing product* media to a variant" contract from the endpoint description, not an arbitrary media row.

### 4. Handler layer (`internal/delivery/http/handler/product_handler.go`)

1. Add one method per endpoint to the existing `ProductHandler` struct, following the exact request-binding/response/error-handling/swagger-annotation style already used for `CreateOption`/`RenameOption`/`ReorderOptions` etc. in this file — read those methods first.
2. Bind path params with `c.Param("id")` / `c.Param("media_id")`, parse with `uuid.Parse`, return `400` on parse failure (copy the existing pattern for `option_id`/`value_id`).
3. Bind JSON bodies with `c.ShouldBindJSON(&input)`, return `400` with validation errors on failure (copy existing pattern).
4. Add Swagger doc comments (`// @Summary`, `// @Router`, etc.) consistent with the other handler methods — run `swag init -g cmd/main.go -o docs --parseInternal` from repo root after adding them.
5. `ProductHandler` will need two new fields: `variantUseCase domain.VariantUseCase` and `mediaUseCase domain.MediaUseCase`, threaded through its constructor — update `NewProductHandler(...)` signature.

### 5. Router (`internal/delivery/http/router.go`)

1. Inside the existing `products := v1.Group("/products")` block, add:
   ```go
   products.POST("/:id/variants", productHandler.CreateVariant)
   products.POST("/:id/variants/bulk", productHandler.BulkCreateVariants)
   products.PATCH("/:id/variants/bulk", productHandler.BulkUpdateVariants)
   products.POST("/:id/variants/bulk-delete", productHandler.BulkDeleteVariants)
   // Static /reorder must be registered before other :variant-scoped params, same reasoning as options/reorder above.
   products.PATCH("/:id/variants/reorder", productHandler.ReorderVariants)

   products.POST("/:id/media", productHandler.CreateMedia)
   products.PATCH("/:id/media/reorder", productHandler.ReorderMedia) // register before /:media_id routes
   products.PATCH("/:id/media/:media_id", productHandler.UpdateMedia)
   products.DELETE("/:id/media/:media_id", productHandler.DeleteMedia)
   ```
2. Add a new top-level group for the `/v1/variants/:id` routes that aren't nested under `/products`:
   ```go
   variants := v1.Group("/variants")
   {
       variants.PATCH("/:id", productHandler.UpdateVariant)
       variants.DELETE("/:id", productHandler.DeleteVariant)
       variants.POST("/:id/restore", productHandler.RestoreVariant)

       variants.POST("/:id/media", productHandler.AttachVariantMedia)
       variants.PATCH("/:id/media/reorder", productHandler.ReorderVariantMedia) // before /:media_id
       variants.DELETE("/:id/media/:media_id", productHandler.DetachVariantMedia)
   }
   ```
3. Double-check route registration order in both groups — Gin panics on ambiguous wildcard/static conflicts at startup, so register every literal segment (`/bulk`, `/bulk-delete`, `/reorder`) before any `:param` segment at the same path depth, exactly as the comment above `options/reorder` already warns.

### 6. Wiring (`internal/wire/container.go`)

1. Construct `variantUseCase := product.NewVariantUseCase(productRepo)` and `mediaUseCase := product.NewMediaUseCase(productRepo)` next to the existing `optionUseCase` construction.
2. Pass both into `handler.NewProductHandler(...)` alongside the existing use cases.
3. No new repository struct is needed since we extended the existing `ProductRepository` implementation — just make sure `productRepo` (already constructed) is reused, not re-instantiated.

### 7. Tests

1. Add `variantUseCase_test.go` and `mediaUseCase_test.go` in `internal/app/product/`, following the mocking/table-test style of `queryUseCase_test.go` and `lifecycleUseCase_test.go` in the same directory (check what mocking approach those use — e.g. a hand-written fake implementing `domain.ProductRepository`, or `testify/mock` — and reuse the same one, don't introduce a second mocking library).
2. Add handler tests to `product_handler_test.go` following its existing pattern (check whether it spins up a `httptest.Server` + `gin.TestMode` and mocks the use case interfaces directly).
3. Cover at minimum: happy path for each endpoint, 404 when product/variant/media doesn't exist, 400 on invalid body/UUID, and the soft-vs-hard delete branch in variant deletion.
4. Run `go test ./...` and ensure everything passes before considering the task done.

### 8. Verification checklist

- [ ] `go build ./...` succeeds
- [ ] `go test ./...` passes
- [ ] `swag init -g cmd/main.go -o docs --parseInternal` regenerates docs without error
- [ ] Manually hit each new endpoint via `air` + curl/Postman against a local Postgres to confirm wiring end-to-end (create product → add variant → attach media → reorder → delete)
- [ ] Confirm route registration order doesn't panic Gin on startup (`go run cmd/main.go`)
