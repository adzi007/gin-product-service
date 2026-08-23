# Task: Split `ProductRepository` (Phase 6)

Source: `issue.md`, "Phase 6 — Split `ProductRepository`".

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
- Phase 4 (aggregate behavior): `Product` has `NewProduct`, `AddVariant`, `Archive`,
  `Restore`; `ProductStatus.CanTransitionTo`; `InventoryLevel.Reserve`.
- Phase 5 (read models): `ProductRepository.FindAll` returns `[]ProductListItem`,
  `FindByID`/`FindByHandle` return `ProductDetail`. `Product` itself only carries
  write-aggregate fields.
- `internal/domain/category.go` is a separate, already-shipped module — **out of
  scope** for this task. Do not touch `category.go` or `categoryRepo.go`.

Today, `internal/domain/product.go` still defines one 30+ method interface,
`ProductRepository` (currently around lines 258-308), covering product header CRUD,
options, option values, variants, variant stock adjustment, variant media, product
media, and variant-media linking — all implemented by a single
`internal/infrastructure/repository/productRepo.go`. Every use case that touches
*any* of this — `optionUc`, `mediaUc`, `variantUc`, plus the header-only
`updateUc`/`deleteUc`/`queryUc`/`insertUc` — depends on the entire interface, even
though (for example) `optionUc` only ever calls 8 of the 30+ methods.

## Goal

Split `ProductRepository` into four narrower interfaces along the sub-resource lines
that already exist in the codebase (options, variants, media), so each use case can
depend only on what it actually calls (ISP):

- `ProductRepository` (stays in `product.go`) — product header CRUD only:
  `Create`, `FindAll`, `FindByID`, `FindByHandle`, `UpdateHeader`, `UpdateStatus`,
  `Delete`.
- `OptionRepository` (new, in `product.go`) — the 9 option/option-value methods.
- `MediaRepository` (new, in `product.go`) — the 9 product-media/variant-media
  methods.
- `VariantRepository` (new, in `variant.go`) — the 11 variant CRUD/lifecycle/stock
  methods.

By the end of this task:
- No single interface has more than ~11 methods.
- `optionUc` depends on `ProductRepository` + `OptionRepository` only.
- `mediaUc` depends on `ProductRepository` + `MediaRepository` + `VariantRepository`
  (it needs `FindVariantByID` for `AttachToVariant`).
- `variantUc` depends on `ProductRepository` + `VariantRepository` + `MediaRepository`
  (it needs `FindMediaByID`/`AttachNewOrExistingVariantMedia` for variant media).
- `insertUc`, `updateUc`, `deleteUc`, `queryUc` are **unchanged** — they only ever
  called plain product-header methods, so they still take a single
  `domain.ProductRepository`.
- `internal/infrastructure/repository/productRepo.go` still has exactly **one**
  concrete struct (`productRepo`) implementing all four interfaces — this task does
  not physically split the implementation file or its private helpers
  (`findProductOptions`, `findProductVariants`, `findProductMedia`, the `Create`/
  `CreateVariantsWithStock` transactions, etc.).

### What this task deliberately does NOT do (out of scope)

- **No separate `InventoryRepository`.** `issue.md`'s Phase 6 proposal lists one, but
  every inventory-touching method (`Create`'s inventory-item/stock-move/
  inventory-level inserts, `CreateVariantsWithStock`, `AdjustVariantStock`) writes to
  inventory tables **inside the same DB transaction** as the product/variant row it's
  attached to, and is only ever called through `ProductRepository.Create` or
  `VariantRepository.CreateVariantsWithStock`/`AdjustVariantStock` — nothing calls
  inventory persistence independently today. Carving out a standalone
  `InventoryRepository` would mean passing a shared transaction across repository
  interfaces, which is a real architectural change, not a mechanical interface split.
  Leave `AdjustVariantStock` and `VariantHasHistory` on `VariantRepository` (that's
  where their only caller, `variantUc`, already reaches them from) and revisit a true
  inventory-transaction boundary as its own follow-up if/when something needs to
  adjust stock without going through a variant use case.
- **No splitting of `productRepo.go` into multiple files/structs.** One struct, one
  DB pool, shared private helpers — only the exported interfaces it satisfies change.
- **No changes to `Product`, `Variant`, `ProductListItem`, `ProductDetail`, or any
  other type shape.** This is purely an interface/wiring change.
- Do not touch `internal/domain/category.go`, `categoryRepo.go`, or anything under
  `internal/app/category/`.

### Why this is lower-risk than it sounds

`internal/infrastructure/repository/productRepo.go` already implements every one of
these methods as a method on the same `*productRepo` struct, and
`internal/app/product/queryUseCase_test.go`'s `fakeProductRepo` already implements
every one of them too. In Go, **one concrete type can satisfy multiple interfaces at
once** — you don't need separate structs or separate fakes. Concretely:

- `repository.NewProductRepo(db)` will return the unexported `*productRepo` type
  instead of `domain.ProductRepository`. Callers (just `wire/container.go`) don't
  need to name that type — `productRepo := repository.NewProductRepo(db)` still
  works via type inference, and that one variable can be passed into every use case
  constructor that now asks for a narrower interface, because `*productRepo`
  implements all four.
- Same story for `fakeProductRepo` in tests: it keeps every method it already has, so
  the same `repo` variable in each test file can be passed to multiple constructor
  parameters unchanged in behavior — only the number of arguments at each
  `New...UseCase(...)` call site changes.

## Step-by-step

### 1. Split the interface in `internal/domain/product.go`

Find the current `ProductRepository` interface (~lines 258-308). Replace it with the
trimmed `ProductRepository` plus two new interfaces, `OptionRepository` and
`MediaRepository`, covering the methods being carved out. Keep every method
signature byte-for-byte identical — only which interface it belongs to changes.

```go
// before
type ProductRepository interface {
	Create(ctx context.Context, params CreateProductParams) (Product, error)
	FindAll(ctx context.Context, params ListProductParams) ([]ProductListItem, int, error)
	FindByID(ctx context.Context, id uuid.UUID) (ProductDetail, error)
	FindByHandle(ctx context.Context, handle string) (ProductDetail, error)

	UpdateHeader(ctx context.Context, id uuid.UUID, input UpdateProductInput) (Product, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status ProductStatus) error
	Delete(ctx context.Context, id uuid.UUID) error

	CreateOption(ctx context.Context, option ProductOption) (ProductOption, error)
	RenameOption(ctx context.Context, productID, optionID uuid.UUID, name string) (ProductOption, error)
	DeleteOption(ctx context.Context, productID, optionID uuid.UUID) error
	ReorderOptions(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	FindOptionByID(ctx context.Context, optionID uuid.UUID) (ProductOption, error)

	CreateOptionValue(ctx context.Context, value ProductOptionValue) (ProductOptionValue, error)
	UpdateOptionValue(ctx context.Context, valueID uuid.UUID, value *string, position *int) (ProductOptionValue, error)
	DeleteOptionValue(ctx context.Context, valueID uuid.UUID) error
	FindOptionValueByID(ctx context.Context, valueID uuid.UUID) (ProductOptionValue, error)

	CreateVariant(ctx context.Context, variant Variant) (Variant, error)
	CreateVariants(ctx context.Context, variants []Variant) ([]Variant, error)
	CreateVariantsWithStock(ctx context.Context, params CreateVariantsParams) ([]Variant, error)
	AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty int) (InventoryLevel, error)
	FindVariantByID(ctx context.Context, variantID uuid.UUID) (Variant, error)
	UpdateVariant(ctx context.Context, variantID uuid.UUID, input UpdateVariantInput) (Variant, error)
	DeleteVariant(ctx context.Context, variantID uuid.UUID, hard bool) error
	BulkDeleteVariants(ctx context.Context, variantIDs []uuid.UUID, hard bool) error
	RestoreVariant(ctx context.Context, variantID uuid.UUID) (Variant, error)
	ReorderVariants(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	VariantHasHistory(ctx context.Context, variantID uuid.UUID) (bool, error)

	CreateProductMedia(ctx context.Context, media []ProductMedia) ([]ProductMedia, error)
	UpdateProductMedia(ctx context.Context, mediaID uuid.UUID, altText *string) (ProductMedia, error)
	DeleteProductMedia(ctx context.Context, mediaID uuid.UUID) error
	ReorderProductMedia(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	FindMediaByID(ctx context.Context, mediaID uuid.UUID) (ProductMedia, error)

	AttachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) (VariantMedia, error)
	DetachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) error
	ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []PositionUpdate) error

	AttachNewOrExistingVariantMedia(ctx context.Context, newMedia []ProductMedia, links []VariantMedia) error
}
```

```go
// after
// ProductRepository is the persistence contract for the product header:
// creating, reading, updating, and deleting the products table row itself.
type ProductRepository interface {
	Create(ctx context.Context, params CreateProductParams) (Product, error)
	FindAll(ctx context.Context, params ListProductParams) ([]ProductListItem, int, error)
	FindByID(ctx context.Context, id uuid.UUID) (ProductDetail, error)
	FindByHandle(ctx context.Context, handle string) (ProductDetail, error)

	UpdateHeader(ctx context.Context, id uuid.UUID, input UpdateProductInput) (Product, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status ProductStatus) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// OptionRepository is the persistence contract for a product's options and
// their option values.
type OptionRepository interface {
	CreateOption(ctx context.Context, option ProductOption) (ProductOption, error)
	RenameOption(ctx context.Context, productID, optionID uuid.UUID, name string) (ProductOption, error)
	DeleteOption(ctx context.Context, productID, optionID uuid.UUID) error
	ReorderOptions(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	FindOptionByID(ctx context.Context, optionID uuid.UUID) (ProductOption, error)

	CreateOptionValue(ctx context.Context, value ProductOptionValue) (ProductOptionValue, error)
	UpdateOptionValue(ctx context.Context, valueID uuid.UUID, value *string, position *int) (ProductOptionValue, error)
	DeleteOptionValue(ctx context.Context, valueID uuid.UUID) error
	FindOptionValueByID(ctx context.Context, valueID uuid.UUID) (ProductOptionValue, error)
}

// MediaRepository is the persistence contract for a product's media gallery
// and its links to variants.
type MediaRepository interface {
	CreateProductMedia(ctx context.Context, media []ProductMedia) ([]ProductMedia, error)
	UpdateProductMedia(ctx context.Context, mediaID uuid.UUID, altText *string) (ProductMedia, error)
	DeleteProductMedia(ctx context.Context, mediaID uuid.UUID) error
	ReorderProductMedia(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	FindMediaByID(ctx context.Context, mediaID uuid.UUID) (ProductMedia, error)

	AttachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) (VariantMedia, error)
	DetachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) error
	ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []PositionUpdate) error

	// AttachNewOrExistingVariantMedia appends media links to a variant during
	// update. It inserts any brand-new media rows (newMedia) into product_media
	// and then inserts the variant_media links; it never touches existing links.
	AttachNewOrExistingVariantMedia(ctx context.Context, newMedia []ProductMedia, links []VariantMedia) error
}
```

Place `OptionRepository` and `MediaRepository` directly after the trimmed
`ProductRepository`, in that order. The variant methods are cut entirely here —
step 2 pastes them into `variant.go`.

Build after this step: `go build ./...` will **fail**. That's expected —
`internal/infrastructure/repository/productRepo.go` still declares
`func NewProductRepo(db database.Database) domain.ProductRepository`, and that
concrete type no longer has `CreateVariant`/etc. required to satisfy the
now-nonexistent variant methods on `ProductRepository` — wait, actually the build
error you'll see is simpler: `productRepo.go`'s `NewProductRepo` return type
assertion will fail to compile because `domain.ProductRepository` no longer declares
the variant/option/media methods your `*productRepo` still implements just fine —
Go interfaces are structural, so **extra** methods on `*productRepo` are never an
error. The real failure is that `NewProductRepo`'s declared return type
`domain.ProductRepository` is now a *smaller* interface than before, which still
compiles. The actual failures you'll see are in `internal/app/product/*.go` and
`*_test.go`, where `optionUc`/`mediaUc`/`variantUc` call methods
(`uc.productRepo.CreateOption(...)`, etc.) that no longer exist on
`domain.ProductRepository`. Don't fix those yet — steps 2-5 do that in order.

### 2. Add `VariantRepository` to `internal/domain/variant.go`

Open `variant.go`. Add the new interface below the existing `VariantUseCase`
interface (don't confuse the two: `VariantUseCase` is the application-layer contract
already in this file; `VariantRepository` is the new persistence-layer contract):

```go
// VariantRepository is the persistence contract for a product's variants,
// including their inventory stock adjustment and lifecycle (soft/hard
// delete, restore).
type VariantRepository interface {
	CreateVariant(ctx context.Context, variant Variant) (Variant, error)
	CreateVariants(ctx context.Context, variants []Variant) ([]Variant, error)
	CreateVariantsWithStock(ctx context.Context, params CreateVariantsParams) ([]Variant, error)
	AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty int) (InventoryLevel, error)
	FindVariantByID(ctx context.Context, variantID uuid.UUID) (Variant, error)
	UpdateVariant(ctx context.Context, variantID uuid.UUID, input UpdateVariantInput) (Variant, error)
	DeleteVariant(ctx context.Context, variantID uuid.UUID, hard bool) error
	BulkDeleteVariants(ctx context.Context, variantIDs []uuid.UUID, hard bool) error
	RestoreVariant(ctx context.Context, variantID uuid.UUID) (Variant, error)
	ReorderVariants(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	// VariantHasHistory reports whether a variant has any stock movement
	// history, which decides soft vs hard deletion.
	VariantHasHistory(ctx context.Context, variantID uuid.UUID) (bool, error)
}
```

`CreateVariantsParams` (defined in `product.go`) and `InventoryLevel` (defined in
`inventory.go`) are both still in package `domain`, so no import changes are needed.

Build after this step: still expected to fail in `internal/app/product/*.go` and
test files — that's what steps 3-5 fix.

### 3. Point `productRepo.go` at the new interfaces

Open `internal/infrastructure/repository/productRepo.go`. The only change is the
constructor's return type — every method on `*productRepo` already has the exact
signatures the new interfaces require, so nothing else in this file changes.

```go
// before
func NewProductRepo(db database.Database) domain.ProductRepository {
	return &productRepo{db: db}
}

// after
func NewProductRepo(db database.Database) *productRepo {
	return &productRepo{db: db}
}
```

Immediately below the `productRepo` struct/`NewProductRepo` block, add compile-time
assertions that `*productRepo` satisfies all four interfaces. These aren't required
for the build to pass, but they turn "I forgot a method" into an error right here
instead of a confusing failure somewhere in `wire/container.go`:

```go
var (
	_ domain.ProductRepository = (*productRepo)(nil)
	_ domain.OptionRepository  = (*productRepo)(nil)
	_ domain.VariantRepository = (*productRepo)(nil)
	_ domain.MediaRepository   = (*productRepo)(nil)
)
```

Build after this step: `go build ./internal/infrastructure/...` should pass.
`go build ./...` as a whole will still fail in `internal/app/product/*.go` (they
call `NewProductRepo`-independent methods on interfaces that no longer have them) and
`internal/wire/container.go` (it declares `productRepo` implicitly via `:=`, so it's
actually fine — the failures are all in `internal/app/product`).

### 4. Update the three use cases that need a narrower dependency plus the split-off ones

**4a. `internal/app/product/optionUseCase.go`.** Add a second field/parameter for
`OptionRepository`, then repoint every call except the one `FindByID` (which stays on
`productRepo` — it's used in `Create` to confirm the product exists before deriving
the next option position):

```go
// before
type optionUc struct {
	productRepo domain.ProductRepository
}

func NewOptionUseCase(productRepo domain.ProductRepository) domain.OptionUseCase {
	return &optionUc{
		productRepo: productRepo,
	}
}

// after
type optionUc struct {
	productRepo domain.ProductRepository
	optionRepo  domain.OptionRepository
}

func NewOptionUseCase(productRepo domain.ProductRepository, optionRepo domain.OptionRepository) domain.OptionUseCase {
	return &optionUc{
		productRepo: productRepo,
		optionRepo:  optionRepo,
	}
}
```

Then, in the method bodies, change every `uc.productRepo.CreateOption(...)`,
`.RenameOption(...)`, `.DeleteOption(...)`, `.ReorderOptions(...)`,
`.CreateOptionValue(...)`, `.UpdateOptionValue(...)`, `.DeleteOptionValue(...)` call
to `uc.optionRepo.<Method>(...)` instead. Leave `uc.productRepo.FindByID(...)` (in
`Create`) untouched. `FindOptionByID`/`FindOptionValueByID` aren't currently called
from this file at all — nothing to change for those two.

**4b. `internal/app/product/mediaUseCase.go`.** Add `MediaRepository` and
`VariantRepository` fields/parameters:

```go
// before
type mediaUc struct {
	productRepo domain.ProductRepository
}

func NewMediaUseCase(productRepo domain.ProductRepository) domain.MediaUseCase {
	return &mediaUc{
		productRepo: productRepo,
	}
}

// after
type mediaUc struct {
	productRepo domain.ProductRepository
	mediaRepo   domain.MediaRepository
	variantRepo domain.VariantRepository
}

func NewMediaUseCase(productRepo domain.ProductRepository, mediaRepo domain.MediaRepository, variantRepo domain.VariantRepository) domain.MediaUseCase {
	return &mediaUc{
		productRepo: productRepo,
		mediaRepo:   mediaRepo,
		variantRepo: variantRepo,
	}
}
```

In the method bodies: `uc.productRepo.FindByID(...)` stays as-is (used in `Create` to
confirm the product exists). `uc.productRepo.FindVariantByID(...)` (in
`AttachToVariant`) becomes `uc.variantRepo.FindVariantByID(...)`. Every other call —
`CreateProductMedia`, `UpdateProductMedia`, `DeleteProductMedia`,
`ReorderProductMedia`, `FindMediaByID` (both call sites), `AttachVariantMedia`,
`DetachVariantMedia`, `ReorderVariantMedia` — becomes `uc.mediaRepo.<Method>(...)`.

**4c. `internal/app/product/variantUseCase.go`.** Add `VariantRepository` and
`MediaRepository` fields/parameters:

```go
// before
type variantUc struct {
	productRepo domain.ProductRepository
}

func NewVariantUseCase(productRepo domain.ProductRepository) domain.VariantUseCase {
	return &variantUc{
		productRepo: productRepo,
	}
}

// after
type variantUc struct {
	productRepo domain.ProductRepository
	variantRepo domain.VariantRepository
	mediaRepo   domain.MediaRepository
}

func NewVariantUseCase(productRepo domain.ProductRepository, variantRepo domain.VariantRepository, mediaRepo domain.MediaRepository) domain.VariantUseCase {
	return &variantUc{
		productRepo: productRepo,
		variantRepo: variantRepo,
		mediaRepo:   mediaRepo,
	}
}
```

In the method bodies: both `uc.productRepo.FindByID(...)` calls (in `Create` and
`BulkCreate`) stay as-is. `CreateVariantsWithStock` (both call sites),
`UpdateVariant` (both call sites), `AdjustVariantStock` (both call sites),
`FindVariantByID`, `VariantHasHistory` (both call sites), `DeleteVariant`,
`BulkDeleteVariants` (both call sites), `RestoreVariant`, `ReorderVariants` all
become `uc.variantRepo.<Method>(...)`. `AttachNewOrExistingVariantMedia` (both call
sites) and the `FindMediaByID` call near the end of the file (in the media-resolution
helper) become `uc.mediaRepo.<Method>(...)`.

**4d. Leave these four files alone** — they already only ever called plain
product-header methods, so their field type, constructor signature, and bodies don't
change at all: `internal/app/product/insertUseCase.go`,
`internal/app/product/deleteUseCase.go`, `internal/app/product/queryUseCase.go`,
`internal/app/product/updateUseCase.go`.

Build after this step: `go build ./internal/app/...` should now pass.
`go build ./...` will still fail in `internal/wire/container.go` and in
`internal/app/product`'s test files — steps 5-6 fix those.

### 5. Update `internal/wire/container.go`

Only the three call sites for `optionUC`, `variantUC`, and `mediaUC` change — pass
the same `productRepo` value as every argument each constructor now takes, since
`*productRepo` (from step 3) satisfies all four interfaces at once:

```go
// before
productRepo := repository.NewProductRepo(db)
productInsertUC := product.NewProductInsertUseCase(productRepo)
productQueryUC := product.NewProductQueryUseCase(productRepo)
productUpdateUC := product.NewProductUpdateUseCase(productRepo)
productDeleteUC := product.NewProductDeleteUseCase(productRepo)
optionUC := product.NewOptionUseCase(productRepo)
variantUC := product.NewVariantUseCase(productRepo)
mediaUC := product.NewMediaUseCase(productRepo)

// after
productRepo := repository.NewProductRepo(db)
productInsertUC := product.NewProductInsertUseCase(productRepo)
productQueryUC := product.NewProductQueryUseCase(productRepo)
productUpdateUC := product.NewProductUpdateUseCase(productRepo)
productDeleteUC := product.NewProductDeleteUseCase(productRepo)
optionUC := product.NewOptionUseCase(productRepo, productRepo)
variantUC := product.NewVariantUseCase(productRepo, productRepo, productRepo)
mediaUC := product.NewMediaUseCase(productRepo, productRepo, productRepo)
```

`productInsertUC`/`productQueryUC`/`productUpdateUC`/`productDeleteUC`'s lines are
unchanged — shown only for context. Nothing else in this file changes.

Build after this step: `go build ./...` should now pass for non-test code. Run
`go vet ./...` too.

### 6. Fix the test call sites

No fake needs new methods — `fakeProductRepo` in
`internal/app/product/queryUseCase_test.go` already implements every method on all
four new interfaces (it implemented the single old `ProductRepository`, which was a
superset). Only the number of arguments at each constructor call changes.

**6a. `internal/app/product/lifecycleUseCase_test.go`.** Every `NewOptionUseCase(repo)`
call (there are 8) becomes `NewOptionUseCase(repo, repo)`. Leave every
`NewProductUpdateUseCase(repo)` and `NewProductDeleteUseCase(repo)` call unchanged.

**6b. `internal/app/product/mediaUseCase_test.go`.** Every `NewMediaUseCase(repo)`
call (there are 10) becomes `NewMediaUseCase(repo, repo, repo)`.

**6c. `internal/app/product/variantUseCase_test.go`.** Every `NewVariantUseCase(repo)`
call (there are 9) becomes `NewVariantUseCase(repo, repo, repo)`.

**6d. `internal/app/product/queryUseCase_test.go`.** `NewProductQueryUseCase(repo)`
and `NewProductInsertUseCase(repo)` calls are unchanged. Optionally, strengthen the
existing compile-time assertion so a future accidental method removal is caught here
too:

```go
// before
var _ domain.ProductRepository = (*fakeProductRepo)(nil)

// after
var (
	_ domain.ProductRepository = (*fakeProductRepo)(nil)
	_ domain.OptionRepository  = (*fakeProductRepo)(nil)
	_ domain.VariantRepository = (*fakeProductRepo)(nil)
	_ domain.MediaRepository   = (*fakeProductRepo)(nil)
)
```

If your editor supports multi-file find/replace, steps 6a-6c are each a single
regex-style replace within one file (`NewXUseCase(repo)` → `NewXUseCase(repo, ...)`)
— just double-check you didn't also touch an unrelated `New...(repo)` call for a
constructor that didn't change (e.g. don't touch
`NewProductUpdateUseCase(repo)`/`NewProductDeleteUseCase(repo)`/
`NewProductQueryUseCase(repo)`/`NewProductInsertUseCase(repo)`).

### 7. Build and verify

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
go test ./internal/infrastructure/repository/... -v
```

If anything else fails to compile that isn't covered above, it's almost certainly
another call site reaching a now-relocated method through the wrong field — grep for
the split-off method names to confirm every call site was moved to the right
sub-repo field:

```bash
grep -rn "productRepo\.\(CreateOption\|RenameOption\|DeleteOption\|ReorderOptions\|CreateOptionValue\|UpdateOptionValue\|DeleteOptionValue\|FindOptionByID\|FindOptionValueByID\)" internal/app/product/
grep -rn "productRepo\.\(CreateVariant\|CreateVariants\|CreateVariantsWithStock\|AdjustVariantStock\|FindVariantByID\|UpdateVariant\|DeleteVariant\|BulkDeleteVariants\|RestoreVariant\|ReorderVariants\|VariantHasHistory\)" internal/app/product/
grep -rn "productRepo\.\(CreateProductMedia\|UpdateProductMedia\|DeleteProductMedia\|ReorderProductMedia\|FindMediaByID\|AttachVariantMedia\|DetachVariantMedia\|ReorderVariantMedia\|AttachNewOrExistingVariantMedia\)" internal/app/product/
```

All three should return **no matches** once step 4 is complete — any hit means a
call site still reaches a split-off method via the `productRepo` field instead of
the new `optionRepo`/`variantRepo`/`mediaRepo` field.

### 8. Manual sanity check

Start the server (`air` or `go run cmd/main.go`) and exercise one endpoint per
use case that changed, confirming behavior is identical to before this task (this is
a wiring change, not a behavior change):

- `POST /api/v1/products/:id/options` and its rename/delete/reorder/value endpoints
  (`optionUc`).
- `POST /api/v1/products/:id/media`, its update/delete/reorder endpoints, and
  `POST /api/v1/variants/:id/media` / detach / reorder (`mediaUc`).
- `POST /api/v1/products/:id/variants`, bulk create, update (including a stock
  change), bulk update, delete, bulk delete, restore, reorder (`variantUc`).
- `GET /api/v1/products`, `GET /api/v1/products/:id`, `POST /api/v1/products`,
  `PATCH /api/v1/products/:id`, archive/restore — these use the untouched
  `insertUc`/`queryUc`/`updateUc`/`deleteUc`, so confirm they still work simply as a
  regression check that `wire/container.go` wiring didn't break anything.

## Notes for whoever picks this up

- Resist the temptation to also rename `productRepo` (the field name used in
  `optionUc`/`mediaUc`/`variantUc`) to something like `headerRepo` "for clarity" —
  it's out of scope and creates unnecessary diff noise. The field holds a
  `domain.ProductRepository` and is named `productRepo`; that's consistent with the
  four untouched use cases and with `internal/wire/container.go`'s local variable.
- Don't try to also give `MediaRepository`/`OptionRepository`/`VariantRepository`
  their own `New...Repo(db)` constructors in the `repository` package. There is
  intentionally only one constructor, `repository.NewProductRepo(db)`, returning the
  single concrete type that satisfies all four interfaces — introducing separate
  constructors would imply separate structs/state, which this task explicitly avoids.
- If a future task *does* need to physically separate the implementation (e.g. to
  move variant persistence into its own file, or eventually its own package), that's
  a bigger, separate change — it would need to decide how the shared private helpers
  (`findProductOptions`, `findProductVariants`, `findProductMedia`) and the
  multi-table transactions in `Create`/`CreateVariantsWithStock` get divided or
  shared across structs. Nothing in this task blocks that from happening later; it
  also doesn't attempt it.
- This task does not touch `internal/domain/inventory.go` at all, and does not
  introduce an `InventoryRepository` — see "What this task deliberately does NOT do"
  above for why.
- This task does not touch value objects (`Quantity`, `Money`) — that's Phase 7.
