# Issue: Product Header lifecycle + Options/Option Values management

## Context

Only `POST /products`, `GET /products`, `GET /products/:id` exist today
([product_handler.go](internal/delivery/http/handler/product_handler.go),
[router.go](internal/delivery/http/router.go)). This issue adds the remaining
Product Header endpoints (update, archive, restore, purge) and the
Options/Option Values sub-resource endpoints from [PRD.md](PRD.md) section 4.1.

Follow the existing `category` module's use case style
([updateUseCase.go](internal/app/category/updateUseCase.go),
[deleteUseCase.go](internal/app/category/deleteUseCase.go)) and the existing
`product` module's handler/repo style
([product_handler.go](internal/delivery/http/handler/product_handler.go),
[productRepo.go](internal/infrastructure/repository/productRepo.go)). Every
new use case constructor must return the `domain` interface type, not the
concrete struct. Every new repo method must wrap its query with
`metrics.ObserveDB("product", "<operation>")(time.Now())`. Every app/repo
method takes `ctx context.Context` first. New UUIDs are generated in the use
case layer, never in the repo.

Endpoints in scope (from PRD.md §4.1):

| Method | Endpoint | Description |
| --- | --- | --- |
| `PATCH` | `/v1/products/:id` | Update product header fields only (title, description, vendor, handle, category_id, status) |
| `DELETE` | `/v1/products/:id` | Archive product (sets `status = archived`) — soft delete |
| `POST` | `/v1/products/:id/restore` | Un-archive product (restore `status`) |
| `DELETE` | `/v1/products/:id/purge` | Hard delete product (guarded endpoint, cascades if safe) |
| `POST` | `/v1/products/:id/options` | Create a new option with initial values |
| `PATCH` | `/v1/products/:id/options/:option_id` | Rename an option |
| `DELETE` | `/v1/products/:id/options/:option_id` | Remove an option and all its values |
| `PATCH` | `/v1/products/:id/options/reorder` | Bulk position update for options |
| `POST` | `/v1/products/:id/options/:option_id/values` | Add a value to an option |
| `PATCH` | `/v1/products/:id/options/:option_id/values/:value_id` | Rename or reorder a value |
| `DELETE` | `/v1/products/:id/options/:option_id/values/:value_id` | Remove a value |

---

## Task 1 — Domain: add interfaces, input types, and errors

File: [internal/domain/product.go](internal/domain/product.go)

1. Add new domain errors near the existing `var (...)` block (around line 169):
   - `ErrOptionNotFound = errors.New("option not found")`
   - `ErrOptionValueNotFound = errors.New("option value not found")`
   - `ErrProductHasVariants` (only if you decide purge should block when variants exist — see Task 5 notes)
2. Add request input structs:
   ```go
   type UpdateProductInput struct {
       Title       *string        `json:"title" validate:"omitempty"`
       Description *string        `json:"description"`
       Vendor      *string        `json:"vendor"`
       Handle      *string        `json:"handle" validate:"omitempty"`
       CategoryID  *int           `json:"categoryId" validate:"omitempty,gt=0"`
       Status      *ProductStatus `json:"status" validate:"omitempty,oneof=draft active archived"`
   }

   type CreateOptionInput struct {
       Name   string   `json:"name" binding:"required" validate:"required"`
       Values []string `json:"values" binding:"required" validate:"required,min=1"`
   }

   type UpdateOptionInput struct {
       Name string `json:"name" binding:"required" validate:"required"`
   }

   type ReorderOptionsInput struct {
       Positions []PositionUpdate `json:"positions" binding:"required" validate:"required,min=1,dive"`
   }

   // PositionUpdate is shared by any reorder endpoint (options today, variants later).
   type PositionUpdate struct {
       ID       uuid.UUID `json:"id" binding:"required" validate:"required"`
       Position int       `json:"position" validate:"gte=0"`
   }

   type CreateOptionValueInput struct {
       Value    string `json:"value" binding:"required" validate:"required"`
       Position int    `json:"position" validate:"gte=0"`
   }

   type UpdateOptionValueInput struct {
       Value    *string `json:"value"`
       Position *int    `json:"position" validate:"omitempty,gte=0"`
   }
   ```
3. Add use case interfaces (near `InsertProductUseCase`/`QueryProductUseCase`):
   ```go
   type UpdateProductUseCase interface {
       Update(ctx context.Context, id uuid.UUID, input UpdateProductInput) (Product, error)
       Archive(ctx context.Context, id uuid.UUID) error
       Restore(ctx context.Context, id uuid.UUID) error
   }

   type DeleteProductUseCase interface {
       Purge(ctx context.Context, id uuid.UUID) error
   }

   type OptionUseCase interface {
       Create(ctx context.Context, productID uuid.UUID, input CreateOptionInput) (ProductOption, error)
       Rename(ctx context.Context, productID, optionID uuid.UUID, input UpdateOptionInput) (ProductOption, error)
       Delete(ctx context.Context, productID, optionID uuid.UUID) error
       Reorder(ctx context.Context, productID uuid.UUID, input ReorderOptionsInput) error
       AddValue(ctx context.Context, productID, optionID uuid.UUID, input CreateOptionValueInput) (ProductOptionValue, error)
       UpdateValue(ctx context.Context, productID, optionID, valueID uuid.UUID, input UpdateOptionValueInput) (ProductOptionValue, error)
       DeleteValue(ctx context.Context, productID, optionID, valueID uuid.UUID) error
   }
   ```
4. Extend `ProductRepository` (around line 239) with the methods the use
   cases above will call:
   ```go
   UpdateHeader(ctx context.Context, id uuid.UUID, fields map[string]any) (Product, error)
   UpdateStatus(ctx context.Context, id uuid.UUID, status ProductStatus) error
   Delete(ctx context.Context, id uuid.UUID) error // hard delete / purge

   CreateOption(ctx context.Context, option ProductOption) (ProductOption, error)
   RenameOption(ctx context.Context, optionID uuid.UUID, name string) (ProductOption, error)
   DeleteOption(ctx context.Context, optionID uuid.UUID) error
   ReorderOptions(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
   FindOptionByID(ctx context.Context, optionID uuid.UUID) (ProductOption, error)

   CreateOptionValue(ctx context.Context, value ProductOptionValue) (ProductOptionValue, error)
   UpdateOptionValue(ctx context.Context, valueID uuid.UUID, value *string, position *int) (ProductOptionValue, error)
   DeleteOptionValue(ctx context.Context, valueID uuid.UUID) error
   FindOptionValueByID(ctx context.Context, valueID uuid.UUID) (ProductOptionValue, error)
   ```
   Note: prefer a small typed struct over `map[string]any` for `UpdateHeader`
   if you want compile-time safety — a `map[string]any` works but loses type
   checking; either is acceptable, pick one and be consistent.

**Why `UpdateHeader` takes only changed fields:** the PRD says PATCH updates
header fields "only" — so this must be a partial update, not a full
overwrite. Model it as pointer fields (`*string`, `*int`, `*ProductStatus`)
in `UpdateProductInput`, translate `nil` = "don't touch this column" in the
repo's SQL (e.g. build a dynamic `SET` clause, same pattern as filter-building
in `productRepo.FindAll`).

---

## Task 2 — Use case: Product header (update / archive / restore / purge)

New files, `internal/app/product/`:

- `updateUseCase.go` — implements `domain.UpdateProductUseCase`
- `deleteUseCase.go` — implements `domain.DeleteProductUseCase`

Follow [updateUseCase.go](internal/app/category/updateUseCase.go) exactly for
structure: a private struct holding `productRepo domain.ProductRepository`,
a constructor `NewProductUpdateUseCase(productRepo domain.ProductRepository) domain.UpdateProductUseCase`,
and methods that log via `logger.L(ctx)` on both error and success paths
(`zap.Error(err)`, relevant IDs).

Steps:
1. `Update(ctx, id, input)`:
   - Validate `id` is resolvable (let the repo return `domain.ErrProductNotFound` if missing — don't pre-check with an extra query).
   - Call `productRepo.UpdateHeader(ctx, id, input)`.
   - Log and return.
2. `Archive(ctx, id)`:
   - Call `productRepo.UpdateStatus(ctx, id, domain.ProductStatusArchived)`.
3. `Restore(ctx, id)`:
   - Call `productRepo.UpdateStatus(ctx, id, domain.ProductStatusActive)`.
   - (PRD says "restore status" — restoring always to `active` is the
     simplest correct behavior; there's no "previous status" tracked in the
     schema.)
4. `Purge(ctx, id)` (in `deleteUseCase.go`):
   - Call `productRepo.Delete(ctx, id)`.
   - This is a hard delete — the PRD calls it "guarded" and "cascades if
     safe". Decide the guard rule with the reviewer before implementing SQL
     (see Task 4, step on `Delete`); a reasonable default is to block purge
     when the product has any variant with an `inventory_item` that has
     non-zero `stock_moves` history, returning a new
     `domain.ErrProductInvalidStatus`-style error — confirm naming with the
     team rather than guessing.

---

## Task 3 — Use case: Options & Option Values

New file: `internal/app/product/optionUseCase.go`, implementing
`domain.OptionUseCase`.

Struct holds `productRepo domain.ProductRepository`. Constructor:
`NewOptionUseCase(productRepo domain.ProductRepository) domain.OptionUseCase`.

Steps per method:
1. `Create(ctx, productID, input)`:
   - Generate `optionID := uuid.Must(uuid.NewV7())` in the use case (per
     CLAUDE.md convention — UUIDs are generated here, not in the repo).
   - Build `domain.ProductOption{ID: optionID, ProductID: productID, Name: input.Name, Position: <next position>}`.
   - For each `input.Values[i]`, generate a `uuid.NewV7()` and build a
     `domain.ProductOptionValue{ID: ..., OptionID: optionID, Value: v, Position: i}`.
   - Call `productRepo.CreateOption(ctx, option)` — the repo method should
     insert the option row plus all its value rows in one call (wrap in a
     transaction at the repo layer per CLAUDE.md's "multi-table writes" rule).
2. `Rename(ctx, productID, optionID, input)`:
   - Call `productRepo.RenameOption(ctx, optionID, input.Name)`.
   - (`productID` is accepted for route consistency/future authorization
     checks; if the repo query already scopes by option ID uniquely, you
     don't strictly need to verify `productID` matches — but do add a
     `WHERE product_id = $2` guard in the SQL so a mismatched product_id in
     the URL 404s instead of silently renaming another product's option.)
3. `Delete(ctx, productID, optionID)`:
   - Call `productRepo.DeleteOption(ctx, optionID)` — repo cascades to
     `product_option_values` (either via `ON DELETE CASCADE` in the schema,
     which you should verify, or an explicit delete in the same transaction).
4. `Reorder(ctx, productID, input)`:
   - Call `productRepo.ReorderOptions(ctx, productID, input.Positions)` — repo
     should update all rows in one transaction (bulk position update, not
     one-request-per-row).
5. `AddValue(ctx, productID, optionID, input)`:
   - Generate a new UUIDv7 for the value.
   - Call `productRepo.CreateOptionValue(ctx, domain.ProductOptionValue{...})`.
6. `UpdateValue(ctx, productID, optionID, valueID, input)`:
   - Call `productRepo.UpdateOptionValue(ctx, valueID, input.Value, input.Position)`.
7. `DeleteValue(ctx, productID, optionID, valueID)`:
   - Call `productRepo.DeleteOptionValue(ctx, valueID)`.

Log every mutating call on success/failure via `logger.L(ctx)`, same as
`category`'s use cases.

---

## Task 4 — Repository: implement the new `ProductRepository` methods

File: [internal/infrastructure/repository/productRepo.go](internal/infrastructure/repository/productRepo.go)

General rules (same as existing methods in this file):
- Every method starts with `defer metrics.ObserveDB("product", "<op>")(time.Now())`.
- Use `pgUUID(...)` / `pgUUIDPtr(...)` helpers already defined at the bottom
  of the file for UUID params.
- Multi-statement writes use `r.db.GetDb().Begin(ctx)` / `tx.Commit(ctx)` /
  `defer tx.Rollback(ctx)`, same shape as `Create`.
- Translate unique-constraint violations with `translateCreateError` where
  relevant (e.g. duplicate option name per product, if you add that
  constraint).

Steps:
1. `UpdateHeader(ctx, id, input domain.UpdateProductInput) (domain.Product, error)`:
   - Build a dynamic `UPDATE products SET ... WHERE id = $n RETURNING ...`
     using the same `conditions []string` / `args []interface{}` /
     `argIdx` pattern as `FindAll`'s filter builder — but for `SET` clauses
     driven by which pointer fields in `input` are non-nil.
   - If zero fields are non-nil, return early (no-op update, or reuse
     `FindByID` to just return current state — decide based on what's
     simplest, don't over-engineer).
   - On no matching row, return `domain.ErrProductNotFound`.
2. `UpdateStatus(ctx, id, status domain.ProductStatus) error`:
   - `UPDATE products SET status = $1, updated_at = now() WHERE id = $2`.
   - Check `RowsAffected()` on the `pgconn.CommandTag`; if zero, return
     `domain.ErrProductNotFound`.
3. `Delete(ctx, id) error` (hard delete / purge):
   - If you decided on a guard (Task 2, step 4), check it first inside the
     same transaction before deleting, then delete in dependency order:
     `variant_media` → `inventory_levels`/`stock_moves`/`inventory_items` (for
     that product's variants) → `variants` → `product_option_values` →
     `product_options` → `product_media` → `products`. Verify actual FK
     constraints in the schema/migrations first — if `ON DELETE CASCADE` is
     already set up for children of `products`, a single
     `DELETE FROM products WHERE id = $1` may be sufficient; don't write
     manual cascade DELETEs for tables that already cascade.
4. `CreateOption(ctx, option domain.ProductOption) (domain.ProductOption, error)`:
   - Transaction: insert into `product_options`, then loop-insert
     `option.Values` into `product_option_values` (same shape as `Create`'s
     step 2, lines 74-95 of this file).
5. `RenameOption(ctx, optionID, name string) (domain.ProductOption, error)`:
   - `UPDATE product_options SET name = $1 WHERE id = $2 RETURNING id, product_id, name, position`.
   - No matching row → `domain.ErrOptionNotFound`.
6. `DeleteOption(ctx, optionID uuid.UUID) error`:
   - Delete `product_option_values WHERE option_id = $1` then
     `product_options WHERE id = $1` in a transaction, unless cascade is
     already configured at the DB level (check migrations first).
7. `ReorderOptions(ctx, productID, positions []domain.PositionUpdate) error`:
   - In a transaction, loop over `positions` and
     `UPDATE product_options SET position = $1 WHERE id = $2 AND product_id = $3`
     (the `product_id` guard prevents cross-product reorder abuse).
8. `FindOptionByID(ctx, optionID) (domain.ProductOption, error)`:
   - Simple `SELECT` by id, `pgx.CollectOneRow` + `RowToStructByName`, map
     `pgx.ErrNoRows` to `domain.ErrOptionNotFound` (same pattern as
     `findProductBy`, lines 382-388).
9. `CreateOptionValue(ctx, value domain.ProductOptionValue) (domain.ProductOptionValue, error)`:
   - Single insert into `product_option_values`, `RETURNING` the row.
10. `UpdateOptionValue(ctx, valueID, value *string, position *int) (domain.ProductOptionValue, error)`:
    - Dynamic `SET` clause like `UpdateHeader`, only touching non-nil fields.
    - No match → `domain.ErrOptionValueNotFound`.
11. `DeleteOptionValue(ctx, valueID) error`:
    - `DELETE FROM product_option_values WHERE id = $1`; zero rows affected
      → `domain.ErrOptionValueNotFound`.
12. `FindOptionValueByID(ctx, valueID) (domain.ProductOptionValue, error)`:
    - Same pattern as step 8.

---

## Task 5 — Handler: wire up HTTP endpoints

File: [internal/delivery/http/handler/product_handler.go](internal/delivery/http/handler/product_handler.go)

1. Add `updateUseCase domain.UpdateProductUseCase`, `deleteUseCase domain.DeleteProductUseCase`,
   `optionUseCase domain.OptionUseCase` fields to `ProductHandler` and thread
   them through `NewProductHandler`.
2. Add handler methods, following `Create`'s pattern for binding + validating
   with `c.ShouldBindJSON` + `validate.Struct` + `fieldValidationMessage`, and
   `GetByID`'s pattern for parsing the `:id` path param with `uuid.Parse`:
   - `Update(c *gin.Context)` — `PATCH /products/:id`. Parse id, bind
     `domain.UpdateProductInput`, call `updateUseCase.Update`.
   - `Archive(c *gin.Context)` — `DELETE /products/:id`. Parse id, call
     `updateUseCase.Archive`, respond `200`/`204` with no body or a status
     message.
   - `Restore(c *gin.Context)` — `POST /products/:id/restore`. Parse id,
     call `updateUseCase.Restore`.
   - `Purge(c *gin.Context)` — `DELETE /products/:id/purge`. Parse id, call
     `deleteUseCase.Purge`.
   - `CreateOption(c *gin.Context)` — `POST /products/:id/options`. Parse
     product id, bind `domain.CreateOptionInput`, call `optionUseCase.Create`.
   - `RenameOption(c *gin.Context)` — `PATCH /products/:id/options/:option_id`.
     Parse both ids, bind `domain.UpdateOptionInput`, call `optionUseCase.Rename`.
   - `DeleteOption(c *gin.Context)` — `DELETE /products/:id/options/:option_id`.
     Parse both ids, call `optionUseCase.Delete`.
   - `ReorderOptions(c *gin.Context)` — `PATCH /products/:id/options/reorder`.
     Parse product id, bind `domain.ReorderOptionsInput`, call `optionUseCase.Reorder`.
   - `AddOptionValue(c *gin.Context)` — `POST /products/:id/options/:option_id/values`.
     Parse ids, bind `domain.CreateOptionValueInput`, call `optionUseCase.AddValue`.
   - `UpdateOptionValue(c *gin.Context)` — `PATCH /products/:id/options/:option_id/values/:value_id`.
     Parse ids, bind `domain.UpdateOptionValueInput`, call `optionUseCase.UpdateValue`.
   - `DeleteOptionValue(c *gin.Context)` — `DELETE /products/:id/options/:option_id/values/:value_id`.
     Parse ids, call `optionUseCase.DeleteValue`.
3. Extend `mapProductError` (line 206) with the new domain errors:
   - `domain.ErrOptionNotFound` → `404`, `"ERR_OPTION_NOT_FOUND"`
   - `domain.ErrOptionValueNotFound` → `404`, `"ERR_OPTION_VALUE_NOT_FOUND"`
4. Add Swagger `godoc` comment blocks above each new handler method,
   matching the style of `Create`/`Fetch`/`GetByID` (lines 30-41, 83-98,
   153-162) — `@Summary`, `@Description`, `@Tags products`, `@Param`,
   `@Success`, `@Failure`, `@Router`.
5. For every route with `:id` (product id), parse with `uuid.Parse` and
   return `400 ERR_VALIDATION` on failure — don't silently 404, that hides a
   client bug behind a misleading not-found.

---

## Task 6 — Router: register the new routes

File: [internal/delivery/http/router.go](internal/delivery/http/router.go)

Inside the existing `products := v1.Group("/products")` block (around
line 59-64), add:

```go
products.PATCH("/:id", productHandler.Update)
products.DELETE("/:id", productHandler.Archive)
products.POST("/:id/restore", productHandler.Restore)
products.DELETE("/:id/purge", productHandler.Purge)

products.POST("/:id/options", productHandler.CreateOption)
products.PATCH("/:id/options/reorder", productHandler.ReorderOptions) // register BEFORE /:option_id routes
products.PATCH("/:id/options/:option_id", productHandler.RenameOption)
products.DELETE("/:id/options/:option_id", productHandler.DeleteOption)
products.POST("/:id/options/:option_id/values", productHandler.AddOptionValue)
products.PATCH("/:id/options/:option_id/values/:value_id", productHandler.UpdateOptionValue)
products.DELETE("/:id/options/:option_id/values/:value_id", productHandler.DeleteOptionValue)
```

**Watch out:** Gin's router treats `/:id/options/reorder` and
`/:id/options/:option_id` as conflicting wildcard/static siblings at the same
segment depth. Register the static `/reorder` path in a way that Gin can
resolve it (Gin v1.8+ handles static-vs-param siblings fine, but verify with
`go run cmd/main.go` + a quick `curl` that a request to
`/api/v1/products/<uuid>/options/reorder` hits `ReorderOptions`, not
`RenameOption` with `option_id="reorder"`). If Gin rejects the route
registration at startup with a panic about conflicting wildcards, that's the
sign to fix — don't just swallow it.

---

## Task 7 — Wire DI container

File: [internal/wire/container.go](internal/wire/container.go)

1. Construct the new use cases:
   ```go
   productUpdateUc := product.NewProductUpdateUseCase(productRepo)
   productDeleteUc := product.NewProductDeleteUseCase(productRepo)
   optionUc := product.NewOptionUseCase(productRepo)
   ```
2. Pass them into `handler.NewProductHandler(insertUc, queryUc, productUpdateUc, productDeleteUc, optionUc)`
   (adjust the constructor signature from Task 5, step 1 to match whatever
   order you pick — keep it consistent with how `category`'s container wiring
   orders its use cases).
3. Update the `Container` struct field types and any place `ProductHandler`
   is constructed (should be just this one place, but grep for
   `NewProductHandler` to confirm no other call site exists).

---

## Task 8 — Tests

1. Unit test each new use case in `internal/app/product/*_test.go` with a
   mock `domain.ProductRepository` (check if `product/queryUseCase_test.go`
   already has a mock repo scaffold you can extend rather than writing a new
   one from scratch).
2. Extend [product_handler_test.go](internal/delivery/http/handler/product_handler_test.go)
   with request/response tests for each new route, covering: happy path,
   validation failure (400), not-found (404).
3. Run `go test ./...` and confirm everything passes before considering this
   done.

---

## Task 9 — Swagger regeneration

After all handlers have their godoc annotations, run from repo root:

```bash
swag init -g cmd/main.go -o docs --parseInternal
```

Confirm the new routes appear in `docs/swagger.json` / `docs/swagger.yaml`
and that `/swagger/index.html` renders them when the server is running.

---

## Open questions to resolve before/while implementing (don't guess silently)

- **Purge guard rule**: what exactly makes a product "unsafe" to hard-delete?
  (Task 2, step 4 / Task 4, step 3). Needs a product decision, not just an
  engineering one.
- **Duplicate option names**: should `POST /products/:id/options` reject a
  second option with the same `name` on the same product? No unique
  constraint currently implied by the schema as read from this file — check
  migrations before assuming either way.
- **Restore target status**: confirmed above as always going to `active`;
  flag if product/business wants different behavior (e.g. restore to
  `draft` if it was never published).
