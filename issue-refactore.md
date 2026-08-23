# Task: Separate read models from the write aggregate (Phase 5)

Source: `issue.md`, "Phase 5 — Separate read models from the write aggregate".

> Audience note: this doc is written so a junior developer or another LLM can execute
> it without re-deriving the plan. Follow the steps in order. Don't skip ahead or
> combine steps — each one is designed to leave the repo in a compilable state so
> mistakes are easy to isolate. Run `go build ./...` after every step.

## Context — what already happened

- Phase 1 (bounded-context split): `internal/domain/product.go`, `variant.go`, and
  `inventory.go` exist as separate files.
- Phase 2 (move HTTP DTOs out of `domain`): no `binding:`/`validate:` tags remain in
  `product.go`/`variant.go`.
- Phase 3 (strip persistence tags): no `db:"..."` tags remain in `product.go`,
  `variant.go`, or `inventory.go`. Row-scanning structs live in
  `internal/infrastructure/repository/model`.
- Phase 4 (aggregate behavior) is done: `Product` has `NewProduct`, `AddVariant`,
  `Archive`, `Restore`; `ProductStatus.CanTransitionTo`; `InventoryLevel.Reserve`.
  `internal/app/product/insertUseCase.go` and `updateUseCase.go` already call these.
- `internal/domain/category.go` is a separate, already-shipped module — **out of
  scope** for this task. Do not touch `category.go` or `categoryRepo.go`.

## Goal

Today `internal/domain/product.go` defines `Product` with three fields that only
exist to serve *read* endpoints, not the create/update aggregate:

```go
type Product struct {
	...
	Thumbnail *ProductThumbnail `json:"thumbnail"`
	...
	Category  ProductCategory   `json:"category"`
	Prices    ProductPrices     `json:"prices"`
	...
}
```

- `Category`/`Prices` are only ever populated by `productRepo.FindAll` (list) and
  `Category` alone by `productRepo.findProductBy` (detail, used by `FindByID`/
  `FindByHandle`) — see `internal/infrastructure/repository/productRepo.go:319-333`
  and `:857-866`.
- `Thumbnail` is only populated by `FindAll`.
- Nothing sets any of the three when creating or updating a product
  (`insertUseCase.go`, `updateUseCase.go` never touch them).

This means every write path (`Create`, `Update`, `Archive`, `Restore`, and every
option/variant/media use case that loads a product via `productRepo.FindByID` just
to check it exists) carries three read-only fields it never uses. This task moves
those fields off `Product` and onto two new **read-model** types that wrap it:

```go
// ProductListItem — the shape returned by GET /products (one row of the list).
type ProductListItem struct {
	Product
	Category  ProductCategory
	Prices    ProductPrices
	Thumbnail *ProductThumbnail
}

// ProductDetail — the shape returned by GET /products/:id and GET /products/:handle.
type ProductDetail struct {
	Product
	Category ProductCategory
}
```

By the end of this task:
- `Product` no longer has `Category`, `Prices`, or `Thumbnail` fields.
- `ProductRepository.FindAll` returns `[]ProductListItem` instead of `[]Product`.
- `ProductRepository.FindByID`/`FindByHandle` return `ProductDetail` instead of
  `Product`.
- `QueryProductUseCase.GetByID`/`GetByHandle` return `domain.ProductDetail`.
- `PaginatedProducts.Data` is `[]ProductListItem`.
- The HTTP response JSON shape for `GET /products` and `GET /products/:id` is
  **byte-for-byte unchanged** — this is a type-level refactor, not a behavior change.
  `Category`/`Prices`/`Thumbnail` keep the exact same `json:"..."` tags they have
  today, just moved to the new wrapper types.

### Why this is lower-risk than it sounds

`Product` is embedded (not referenced) in both new types, so every existing call
site that does `product.ID`, `product.Status`, `product.Archive()`, etc. keeps
compiling unchanged — Go promotes embedded fields and methods automatically. Go
also promotes **pointer-receiver methods on a value-embedded field** as long as the
outer value is addressable, which every `product, err := uc.productRepo.FindByID(...)`
local variable is. Concretely:

- `internal/app/product/updateUseCase.go`'s `Archive`/`Restore` call
  `product.Archive()` / `product.Restore()` on the result of `FindByID`. Once
  `FindByID` returns `ProductDetail`, `product.Archive()` still works with **zero
  code changes** in that file, because `ProductDetail` embeds `Product` by value and
  `Archive`/`Restore` have pointer receivers.
- `internal/app/product/mediaUseCase.go`, `optionUseCase.go`, `variantUseCase.go` all
  call `uc.productRepo.FindByID(...)` and only ever read `product.Options` /
  `product.Variants` afterward — these also need **zero code changes**.

Only three places actually need edits: the domain interfaces/types, the Postgres
repository implementation, and the HTTP handler's detail-view mapping function
(`toProductDetailData`) plus its test fakes. This task does NOT include: splitting
`ProductRepository` (Phase 6), value objects like `Quantity`/`Money` (Phase 7), or
any change to `ListProductParams`'s fields (it's already a plain filter/pagination
struct with no domain-object fields — moving it to the same file as the new types is
a pure organizational move, not a behavioral one). Do not touch
`internal/domain/category.go`, `categoryRepo.go`, or anything under
`internal/app/category/`.

## Step-by-step

### 1. Create the read-model file

Create `internal/domain/product_readmodel.go`:

```go
package domain

// ProductListItem is the read-model shape for a single row returned by
// GET /products. It embeds Product for the fields shared with the write
// aggregate and adds projections that only exist at query time: the joined
// category reference, the computed min/max variant price range, and the
// primary gallery image.
type ProductListItem struct {
	Product
	Category  ProductCategory   `json:"category"`
	Prices    ProductPrices     `json:"prices"`
	Thumbnail *ProductThumbnail `json:"thumbnail"`
}

// ProductDetail is the read-model shape for a single product returned by
// GET /products/:id and GET /products/:handle. It embeds Product and adds
// the joined category reference consumed by the detail view.
type ProductDetail struct {
	Product
	Category ProductCategory `json:"category"`
}
```

Do not move `ProductCategory`, `ProductPrices`, `ProductThumbnail`,
`ListProductParams`, or `PaginatedProducts` into this file yet — that happens in
step 2, once you're editing `product.go` anyway, to keep this step a pure addition.

Build after this step: `go build ./...` must still pass (the new types are unused
so far, which is fine).

### 2. Remove the read-only fields from `Product`, relocate the shape types

Open `internal/domain/product.go`.

**2a.** In the `Product` struct (currently lines 40-56), delete the `Thumbnail`,
`Category`, and `Prices` fields:

```go
// before
type Product struct {
	ID          uuid.UUID         `json:"id"`
	Handle      string            `json:"handle"`
	Title       string            `json:"title"`
	Status      ProductStatus     `json:"status"`
	Thumbnail   *ProductThumbnail `json:"thumbnail"`
	Description *string           `json:"description,omitempty"`
	Vendor      *string           `json:"vendor,omitempty"`
	CategoryID  int               `json:"-"`
	Category    ProductCategory   `json:"category"`
	Prices      ProductPrices     `json:"prices"`
	Options     []ProductOption   `json:"options,omitempty"`
	Variants    []Variant         `json:"variants,omitempty"`
	Media       []ProductMedia    `json:"media,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   *time.Time        `json:"updated_at,omitempty"`
}

// after
type Product struct {
	ID          uuid.UUID       `json:"id"`
	Handle      string          `json:"handle"`
	Title       string          `json:"title"`
	Status      ProductStatus   `json:"status"`
	Description *string         `json:"description,omitempty"`
	Vendor      *string         `json:"vendor,omitempty"`
	CategoryID  int             `json:"-"`
	Options     []ProductOption `json:"options,omitempty"`
	Variants    []Variant       `json:"variants,omitempty"`
	Media       []ProductMedia  `json:"media,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
}
```

**2b.** Cut the `ProductCategory`, `ProductPrices`, and `ProductThumbnail` type
definitions (currently lines 115-138, right after `AddVariant`) out of `product.go`
and paste them into `internal/domain/product_readmodel.go` from step 1, above the
`ProductListItem`/`ProductDetail` types you already added there. Keep their doc
comments as-is — they're still accurate, just describe read-model types now instead
of `Product` fields.

**2c.** Cut `ListProductParams` and `PaginatedProducts` (currently lines 242-263)
out of `product.go` and paste them into `product_readmodel.go` too, directly below
the types from 2b. Update `PaginatedProducts.Data`'s type while you're there:

```go
// before
type PaginatedProducts struct {
	Data       []Product `json:"data"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	PerPage    int       `json:"per_page"`
	TotalPages int       `json:"total_pages"`
}

// after
type PaginatedProducts struct {
	Data       []ProductListItem `json:"data"`
	Total      int               `json:"total"`
	Page       int               `json:"page"`
	PerPage    int               `json:"per_page"`
	TotalPages int               `json:"total_pages"`
}
```

Build after this step: `go build ./...` will **fail** — that's expected. You should
see errors in `internal/infrastructure/repository/productRepo.go` (setting
`.Category`/`.Prices`/`.Thumbnail` on a `domain.Product` that no longer has those
fields) and possibly in test files. Do not fix those yet; steps 3-6 do that in order
so you can tell which change fixed which error.

### 3. Update the two interfaces in `product.go`

**3a.** `ProductRepository` (currently lines 311-360): change `FindAll`,
`FindByID`, `FindByHandle`:

```go
// before
FindAll(ctx context.Context, params ListProductParams) ([]Product, int, error)
FindByID(ctx context.Context, id uuid.UUID) (Product, error)
FindByHandle(ctx context.Context, handle string) (Product, error)

// after
FindAll(ctx context.Context, params ListProductParams) ([]ProductListItem, int, error)
FindByID(ctx context.Context, id uuid.UUID) (ProductDetail, error)
FindByHandle(ctx context.Context, handle string) (ProductDetail, error)
```

**3b.** `QueryProductUseCase` (currently lines 265-270): change `GetByID`/
`GetByHandle`:

```go
// before
type QueryProductUseCase interface {
	FindAll(ctx context.Context, params ListProductParams) (PaginatedProducts, error)
	GetByID(ctx context.Context, id uuid.UUID) (Product, error)
	GetByHandle(ctx context.Context, handle string) (Product, error)
}

// after
type QueryProductUseCase interface {
	FindAll(ctx context.Context, params ListProductParams) (PaginatedProducts, error)
	GetByID(ctx context.Context, id uuid.UUID) (ProductDetail, error)
	GetByHandle(ctx context.Context, handle string) (ProductDetail, error)
}
```

Leave every other method on both interfaces untouched — this task does not split
`ProductRepository` (that's Phase 6).

### 4. Update `internal/app/product/queryUseCase.go`

Open the file. `GetByID`/`GetByHandle`'s declared return type must match the
interface change from step 3b:

```go
// before
func (uc *queryProductUc) GetByID(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	data, err := uc.productRepo.FindByID(ctx, id)
	if err != nil {
		if err != domain.ErrProductNotFound {
			logger.L(ctx).Error("find product by id failed", zap.Error(err), zap.String("id", id.String()))
		}
		return domain.Product{}, err
	}
	...
}

// after
func (uc *queryProductUc) GetByID(ctx context.Context, id uuid.UUID) (domain.ProductDetail, error) {
	data, err := uc.productRepo.FindByID(ctx, id)
	if err != nil {
		if err != domain.ErrProductNotFound {
			logger.L(ctx).Error("find product by id failed", zap.Error(err), zap.String("id", id.String()))
		}
		return domain.ProductDetail{}, err
	}
	...
}
```

Do the same for `GetByHandle` (change both `domain.Product` occurrences to
`domain.ProductDetail`). `FindAll` needs **no changes** — it already just forwards
whatever `uc.productRepo.FindAll` returns into `PaginatedProducts.Data`, and both
sides of that assignment are now `[]domain.ProductListItem`.

Build after this step: still expected to fail in `productRepo.go` and possibly
handler/test files — keep going.

### 5. Update `internal/infrastructure/repository/productRepo.go`

This is the step with real logic changes. Go slowly.

**5a. `FindAll`** (currently lines 208-338). Change the signature and the loop body
that builds the result slice:

```go
// before
func (r *productRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.Product, int, error) {
	...
	products := make([]domain.Product, 0, len(listRows))
	for _, row := range listRows {
		p := row.Product.ToDomain()
		if row.CategorySlug != nil {
			p.Category.Slug = *row.CategorySlug
		}
		if row.CategoryName != nil {
			p.Category.Name = *row.CategoryName
		}
		p.Prices.StartPrice = row.StartPrice
		p.Prices.MaxPrice = row.MaxPrice
		if row.ThumbnailURL != nil {
			p.Thumbnail = &domain.ProductThumbnail{
				Type:    *row.ThumbnailType,
				URL:     *row.ThumbnailURL,
				AltText: row.ThumbnailAltText,
			}
		}
		products = append(products, p)
	}

	return products, total, nil
}

// after
func (r *productRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.ProductListItem, int, error) {
	...
	products := make([]domain.ProductListItem, 0, len(listRows))
	for _, row := range listRows {
		item := domain.ProductListItem{Product: row.Product.ToDomain()}
		if row.CategorySlug != nil {
			item.Category.Slug = *row.CategorySlug
		}
		if row.CategoryName != nil {
			item.Category.Name = *row.CategoryName
		}
		item.Prices.StartPrice = row.StartPrice
		item.Prices.MaxPrice = row.MaxPrice
		if row.ThumbnailURL != nil {
			item.Thumbnail = &domain.ProductThumbnail{
				Type:    *row.ThumbnailType,
				URL:     *row.ThumbnailURL,
				AltText: row.ThumbnailAltText,
			}
		}
		products = append(products, item)
	}

	return products, total, nil
}
```

Everything above the loop (query building, `pgx.CollectRows`) is unchanged — only
the loop body and the two `[]domain.Product` occurrences in the signature/`make`
call change.

**5b. `FindByID` / `FindByHandle`** (currently lines 341-352). Only the return type
in the signature changes — the bodies just forward to `findProductBy`:

```go
// before
func (r *productRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	defer metrics.ObserveDB("product", "find_by_id")(time.Now())
	return r.findProductBy(ctx, "products.id = $1", pgUUID(id))
}

func (r *productRepo) FindByHandle(ctx context.Context, handle string) (domain.Product, error) {
	...
}

// after
func (r *productRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.ProductDetail, error) {
	defer metrics.ObserveDB("product", "find_by_id")(time.Now())
	return r.findProductBy(ctx, "products.id = $1", pgUUID(id))
}

func (r *productRepo) FindByHandle(ctx context.Context, handle string) (domain.ProductDetail, error) {
	...
}
```

**5c. `findProductBy`** (currently lines 825-887) — the private helper both of the
above call. Change its return type and the local variable it builds:

```go
// before
func (r *productRepo) findProductBy(ctx context.Context, predicate string, arg interface{}) (domain.Product, error) {
	...
	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[productDetailRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, domain.ErrProductNotFound
		}
		return domain.Product{}, err
	}

	product := row.Product.ToDomain()
	if row.CategorySlug != nil {
		product.Category.Slug = *row.CategorySlug
	}
	if row.CategoryName != nil {
		product.Category.Name = *row.CategoryName
	}
	if row.CategoryId != nil {
		product.Category.Id = *row.CategoryId
	}

	options, err := r.findProductOptions(ctx, product.ID)
	if err != nil {
		return domain.Product{}, err
	}
	product.Options = options

	variants, err := r.findProductVariants(ctx, product.ID)
	if err != nil {
		return domain.Product{}, err
	}
	product.Variants = variants

	media, err := r.findProductMedia(ctx, product.ID)
	if err != nil {
		return domain.Product{}, err
	}
	product.Media = media

	return product, nil
}

// after
func (r *productRepo) findProductBy(ctx context.Context, predicate string, arg interface{}) (domain.ProductDetail, error) {
	...
	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[productDetailRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductDetail{}, domain.ErrProductNotFound
		}
		return domain.ProductDetail{}, err
	}

	detail := domain.ProductDetail{Product: row.Product.ToDomain()}
	if row.CategorySlug != nil {
		detail.Category.Slug = *row.CategorySlug
	}
	if row.CategoryName != nil {
		detail.Category.Name = *row.CategoryName
	}
	if row.CategoryId != nil {
		detail.Category.Id = *row.CategoryId
	}

	options, err := r.findProductOptions(ctx, detail.ID)
	if err != nil {
		return domain.ProductDetail{}, err
	}
	detail.Options = options

	variants, err := r.findProductVariants(ctx, detail.ID)
	if err != nil {
		return domain.ProductDetail{}, err
	}
	detail.Variants = variants

	media, err := r.findProductMedia(ctx, detail.ID)
	if err != nil {
		return domain.ProductDetail{}, err
	}
	detail.Media = media

	return detail, nil
}
```

Note `detail.ID`, `detail.Options`, `detail.Variants`, `detail.Media` all resolve to
the embedded `Product`'s fields via promotion — you're not adding new fields to
`ProductDetail`, just renaming the local variable from `product` to `detail` and
changing its type. `findProductOptions`/`findProductVariants`/`findProductMedia`
themselves are untouched — they take a `uuid.UUID` and don't know about `Product` or
`ProductDetail` at all.

**5d.** Everything else in `productRepo.go` (`Create`, `UpdateHeader`,
`AdjustVariantStock`, all the option/variant/media methods, `productListRow`,
`productDetailRow`) is untouched. In particular, do **not** touch `Create` — it
still builds and returns a plain `domain.Product`, which is correct: creating a
product doesn't need category/price/thumbnail data.

Build after this step: `go build ./...` should now fail only in
`internal/delivery/http/handler/product_handler.go` and possibly test files.

### 6. Update the handler

Open `internal/delivery/http/handler/product_handler.go`. Only `toProductDetailData`
needs a signature change — it's the only place that reads `p.Category` for a value
now sourced from `ProductDetail` instead of `Product`:

```go
// before
func toProductDetailData(p domain.Product) productDetailData {

// after
func toProductDetailData(p domain.ProductDetail) productDetailData {
```

The function body (currently lines 1590-1620+) does not need any other change: `p.ID`,
`p.Handle`, `p.Category.Id`, `p.Category.Slug`, `p.Category.Name`, `p.Options`,
`p.Variants` all still resolve correctly via embedding/promotion.

Do **not** change `toProductData` (used for the create/update response) — it takes
`domain.Product` and never reads `Category`/`Prices`/`Thumbnail`, so it's unaffected.
Its two call sites (`toProductData(created)` from `insertUseCase.Create`,
`toProductData(updated)` from `updateUseCase.Update`) still receive plain
`domain.Product` values, since those use cases' return types didn't change.

Build after this step: `go build ./...` should now pass for non-test code. Run
`go vet ./...` too.

### 7. Fix the test fakes

**7a.** Open `internal/app/product/queryUseCase_test.go`. Update `fakeProductRepo`'s
field types and the three methods this task touched:

```go
// field declarations — before
findAllData   []domain.Product
...
findByIDData    domain.Product

// after
findAllData   []domain.ProductListItem
...
findByIDData    domain.ProductDetail
```

```go
// before
func (f *fakeProductRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.Product, int, error) {
	f.findAllParams = params
	return f.findAllData, f.findAllTotal, f.findAllErr
}

func (f *fakeProductRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	if f.findByIDErr != nil {
		return domain.Product{}, f.findByIDErr
	}
	if f.findByIDData.ID != uuid.Nil {
		return f.findByIDData, nil
	}
	return domain.Product{ID: id}, nil
}

func (f *fakeProductRepo) FindByHandle(ctx context.Context, handle string) (domain.Product, error) {
	if f.findByHandleErr != nil {
		return domain.Product{}, f.findByHandleErr
	}
	return domain.Product{Handle: handle}, nil
}

// after
func (f *fakeProductRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.ProductListItem, int, error) {
	f.findAllParams = params
	return f.findAllData, f.findAllTotal, f.findAllErr
}

func (f *fakeProductRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.ProductDetail, error) {
	if f.findByIDErr != nil {
		return domain.ProductDetail{}, f.findByIDErr
	}
	if f.findByIDData.ID != uuid.Nil {
		return f.findByIDData, nil
	}
	return domain.ProductDetail{Product: domain.Product{ID: id}}, nil
}

func (f *fakeProductRepo) FindByHandle(ctx context.Context, handle string) (domain.ProductDetail, error) {
	if f.findByHandleErr != nil {
		return domain.ProductDetail{}, f.findByHandleErr
	}
	return domain.ProductDetail{Product: domain.Product{Handle: handle}}, nil
}
```

**7b.** Fix the three test functions that construct `findAllData` with the old
element type — `TestQueryProductUseCase_FindAll_AppliesDefaults`,
`TestQueryProductUseCase_FindAll_ComputesTotalPages`, and
`TestQueryProductUseCase_FindAll_PassesFiltersThrough` each have a line like:

```go
// before
findAllData:  []domain.Product{{}},   // or []domain.Product{{}, {}}

// after
findAllData:  []domain.ProductListItem{{}},   // or []domain.ProductListItem{{}, {}}
```

Everything else in this test file (`got.ID`, `got.Handle` assertions in
`TestQueryProductUseCase_GetByID_ReturnsProduct` /
`TestQueryProductUseCase_GetByHandle_ReturnsProduct`) needs **no changes** — those
fields resolve the same way through the embedded `Product`.

**7c.** Open `internal/delivery/http/handler/product_handler_test.go`. Two tests
construct a `domain.Product{...}` literal with a `Category` field directly —
that field no longer exists on `Product`, so these need to wrap in `ProductDetail`/
`ProductListItem`:

```go
// TestToProductDetailData_CategoryAndStock — before
p := domain.Product{
	ID:       uuid.New(),
	Handle:   "ergonomic-cotton-hoodie",
	Title:    "Ergonomic Cotton Hoodie",
	Status:   domain.ProductStatusActive,
	Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
	Variants: []domain.Variant{ ... },
}

// after
p := domain.ProductDetail{
	Product: domain.Product{
		ID:       uuid.New(),
		Handle:   "ergonomic-cotton-hoodie",
		Title:    "Ergonomic Cotton Hoodie",
		Status:   domain.ProductStatusActive,
		Variants: []domain.Variant{ ... },
	},
	Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
}
```

Apply the same wrap-in-`ProductDetail` change to
`TestToProductDetailData_JSONOmitsCategoryID`'s literal.

`TestProductListJSON_CategoryAndPrices` marshals a `domain.Product` directly (not
through `toProductDetailData`) to assert on the list JSON shape — since list items
are no longer plain `Product`, change what it builds and marshals:

```go
// before
p := domain.Product{
	ID:       uuid.New(),
	Handle:   "ergonomic-cotton-hoodie",
	Title:    "Ergonomic Cotton Hoodie",
	Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
	Prices:   domain.ProductPrices{StartPrice: decimal.NewFromFloat(29.99), MaxPrice: decimal.NewFromFloat(59.99)},
}
raw, err := json.Marshal(p)

// after
item := domain.ProductListItem{
	Product: domain.Product{
		ID:     uuid.New(),
		Handle: "ergonomic-cotton-hoodie",
		Title:  "Ergonomic Cotton Hoodie",
	},
	Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
	Prices:   domain.ProductPrices{StartPrice: decimal.NewFromFloat(29.99), MaxPrice: decimal.NewFromFloat(59.99)},
}
raw, err := json.Marshal(item)
```

The rest of that test (the `strings.Contains` assertions) needs no changes — the
expected JSON substrings (`"category"`, `"slug":"apparel"`, `"prices"`,
`"startPrice"`, etc.) are unchanged, since the field tags didn't change, only which
struct they live on.

### 8. Build and verify

```bash
go build ./...
go vet ./...
go test ./...
```

All must pass. Then also run, and read the output rather than just the pass/fail
line:

```bash
go test ./internal/domain/... -v
go test ./internal/app/product/... -v
go test ./internal/delivery/http/handler/... -v
```

If anything else fails to compile that isn't covered above, it's almost certainly
another spot constructing a `domain.Product{...}` literal with `Category`/`Prices`/
`Thumbnail` set — grep for those three field names across `internal/` to be sure you
caught every occurrence:

```bash
grep -rn "Category:\|Prices:\|Thumbnail:" internal/ --include=*.go
```

### 9. Manual sanity check

Start the server (`air` or `go run cmd/main.go`) and exercise:
- `GET /api/v1/products` — response `data[]` items must still contain `category`,
  `prices` (with `startPrice`/`maxPrice`), and `thumbnail`, and must still omit
  `category_id`. Compare against the response shape before this change (or against
  `TestProductListJSON_CategoryAndPrices`'s assertions) — it must be identical.
- `GET /api/v1/products/:id` and `GET /api/v1/products/:handle` — response `data`
  must still contain `category` (with `id`/`slug`/`name`... check
  `productCategoryData` in the handler for the exact exposed shape) and must still
  omit `category_id`.
- `POST /api/v1/products` (create) and `PATCH /api/v1/products/:id` (update) —
  responses are built via `toProductData`, untouched by this task; confirm they
  still look the same as before.
- Archive → Restore a product (exercises `updateUseCase.go`, which calls
  `productRepo.FindByID` and then `product.Archive()`/`product.Restore()` on the
  result) — must behave exactly as before Phase 5, since that code path needed zero
  edits.

## Notes for whoever picks this up

- Resist the temptation to also give `ProductListItem`/`ProductDetail` their own
  constructors or validation — they're pure read-model wrappers assembled by the
  repository from query results, not aggregates with invariants. Nothing about them
  needs to be "valid" independent of what the SQL query returned.
- Don't try to unify `ProductListItem` and `ProductDetail` into one type "since
  they're similar." They intentionally diverge (list has `Prices`/`Thumbnail`,
  detail doesn't) because they're populated by two different queries with two
  different costs — `FindAll`'s query joins a price-aggregation subquery per row,
  which `findProductBy` doesn't need for a single-product fetch. Collapsing them
  would force one query to do unnecessary work for the other's caller.
- Don't move `InsertProductUseCase`, `UpdateProductUseCase`, or their input types —
  they're already correctly scoped to the write side and don't reference the three
  fields this task removes.
- If you find another place outside what steps 5-7 covered that reads
  `product.Category`/`.Prices`/`.Thumbnail` off a `domain.Product` (as opposed to a
  `ProductDetail`/`ProductListItem`), it means there's an undiscovered call site —
  re-run the grep in step 8 rather than guessing; do not silently add the fields
  back onto `Product` to make it compile.
- This task does not change `internal/wire/container.go` — `NewProductQueryUseCase`,
  `NewProductRepository`, etc. are constructed the same way; only the types flowing
  through the interfaces they already return changed.
