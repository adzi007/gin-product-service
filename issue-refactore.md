# Task: Strip persistence tags from domain entities (Phase 3)

Source: `issue.md`, "Phase 3 — Strip persistence tags from entities".

> Audience note: this doc is written so a junior developer or another LLM can execute
> it without re-deriving the plan. Follow the steps in order. Don't skip ahead or
> combine steps — each one is designed to leave the repo in a compilable (or
> intentionally-broken-with-a-known-fix) state so mistakes are easy to isolate.

## Context — what already happened

- Phase 1 (bounded-context split) is done: `internal/domain/product.go`,
  `internal/domain/variant.go`, and `internal/domain/inventory.go` already exist as
  separate files.
- Phase 2 (move HTTP DTOs out of `domain`) is done: `internal/domain/product.go` and
  `internal/domain/variant.go` have no `binding:`/`validate:` tags. Do not reopen that
  work.
- `internal/domain/category.go` still has `binding:`/`validate:` tags — that's a
  pre-existing, separate module, **out of scope** for this task. Do not touch
  `category.go`.

## Goal

Right now, structs in `internal/domain/product.go`, `internal/domain/variant.go`, and
`internal/domain/inventory.go` carry `db:"..."` tags used by
`pgx.RowToStructByName`/`pgx.CollectRows` in
`internal/infrastructure/repository/productRepo.go`. Persistence/column-mapping is an
infrastructure concern, not a domain concern (see `CLAUDE.md`'s Architecture section:
domain should contain "entity structs and interface definitions only"). Today a
column rename forces editing the domain package.

By the end of this task:
- No struct in `internal/domain/product.go`, `internal/domain/variant.go`, or
  `internal/domain/inventory.go` has a `db:"..."` tag (including `db:"-"`).
- A new package `internal/infrastructure/repository/model` holds row structs with
  `db:"..."` tags that `pgx.RowToStructByName` scans into.
- Every repository function that currently scans directly into a `domain.*` struct
  scans into the matching `model.*` struct instead, then converts it to `domain.*` via
  a `ToDomain()` method.
- Use case signatures, handler signatures, and JSON response shapes are **unchanged**.
  This is a pure "move the tag, add a conversion" refactor — no behavior change.

This task does NOT include: splitting `ProductRepository` (Phase 6), adding aggregate
methods (Phase 4), introducing value objects like `Quantity`/`Money` (Phase 7), or
moving read-model structs like `ProductCategory`/`PaginatedProducts` (Phase 5). Do not
touch those in this PR. Do not touch `internal/domain/category.go` or
`internal/infrastructure/repository/categoryRepo.go`.

## Structs in scope

From `internal/domain/product.go`:
- `Product` (has both physical-column fields and `db:"-"` computed fields — see step 2)
- `ProductOption`
- `ProductOptionValue`
- `ProductMedia`

From `internal/domain/variant.go`:
- `Variant`
- `VariantMedia`

From `internal/domain/inventory.go`:
- `InventoryItem` — has `db:"..."` tags but is **never** scanned via
  `pgx.RowToStructByName` (confirmed via grep — it's only ever constructed in Go code
  and inserted with explicit positional args, e.g.
  `internal/app/product/insertUseCase.go:129`, and
  `internal/infrastructure/repository/productRepo.go:131`). No `model` struct is
  needed for it — just delete its `db:"..."` tags.
- `StockMove` — same situation as `InventoryItem`: constructed in Go
  (`internal/app/product/insertUseCase.go:139`,
  `internal/app/product/variantUseCase.go:374`), inserted with explicit positional
  args, never scanned. Just delete its `db:"..."` tags.
- `InventoryLevel` — **is** scanned via `pgx.RowToStructByName` (see step 3), so it
  needs a `model` counterpart.

Everything else in these three files (interfaces, `ProductStatus`, plain input/params
structs like `CreateProductInput`, `CreateProductParams`, etc.) has no `db:"..."` tag
today — leave it untouched.

## Step-by-step

### 1. Create the new package

Create `internal/infrastructure/repository/model/` with three files, mirroring the
domain file split:
- `product_model.go`
- `variant_model.go`
- `inventory_model.go`

Each file starts with `package model`.

### 2. Define row structs with `db` tags only, plus a `ToDomain()` method

For each struct below, define a `model` counterpart: same field names/types, only the
`db:"..."` tag (drop `json:"..."` — nothing in `model` is ever serialized to JSON),
plus a `ToDomain()` method that builds the matching `domain.*` value.

**`model.Product`** (`product_model.go`) — only the physical columns. `Product` in
`domain` also has `Thumbnail`, `Category`, `Prices`, `Options`, `Variants`, `Media`
fields tagged `db:"-"` today — those are populated by application code *after* the
row scan (see `productRepo.go:316-333` for an example), never scanned directly, so
they are **not** part of `model.Product` at all:

```go
// internal/infrastructure/repository/model/product_model.go
package model

import (
	"time"

	"github.com/google/uuid"
	"gin-product-service/internal/domain"
)

type Product struct {
	ID          uuid.UUID  `db:"id"`
	Handle      string     `db:"handle"`
	Title       string     `db:"title"`
	Status      string     `db:"status"`
	Description *string    `db:"description"`
	Vendor      *string    `db:"vendor"`
	CategoryID  int        `db:"category_id"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`
}

func (m Product) ToDomain() domain.Product {
	return domain.Product{
		ID:          m.ID,
		Handle:      m.Handle,
		Title:       m.Title,
		Status:      domain.ProductStatus(m.Status),
		Description: m.Description,
		Vendor:      m.Vendor,
		CategoryID:  m.CategoryID,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

type ProductOption struct {
	ID        uuid.UUID `db:"id"`
	ProductID uuid.UUID `db:"product_id"`
	Name      string    `db:"name"`
	Position  int       `db:"position"`
}

func (m ProductOption) ToDomain() domain.ProductOption {
	return domain.ProductOption{
		ID:        m.ID,
		ProductID: m.ProductID,
		Name:      m.Name,
		Position:  m.Position,
	}
}

type ProductOptionValue struct {
	ID       uuid.UUID `db:"id"`
	OptionID uuid.UUID `db:"option_id"`
	Value    string    `db:"value"`
	Position int       `db:"position"`
}

func (m ProductOptionValue) ToDomain() domain.ProductOptionValue {
	return domain.ProductOptionValue{
		ID:       m.ID,
		OptionID: m.OptionID,
		Value:    m.Value,
		Position: m.Position,
	}
}

type ProductMedia struct {
	ID        uuid.UUID  `db:"id"`
	ProductID uuid.UUID  `db:"product_id"`
	Type      string     `db:"type"`
	URL       string     `db:"url"`
	AltText   *string    `db:"alt_text"`
	Position  int        `db:"position"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

func (m ProductMedia) ToDomain() domain.ProductMedia {
	return domain.ProductMedia{
		ID:        m.ID,
		ProductID: m.ProductID,
		Type:      m.Type,
		URL:       m.URL,
		AltText:   m.AltText,
		Position:  m.Position,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
```

Check `go.mod` for the exact module path before writing the import — use whatever
`module` line says (this repo's is `gin-product-service`, so
`gin-product-service/internal/domain`).

**`model.Variant` / `model.VariantMedia`** (`variant_model.go`) — same pattern.
`domain.Variant.Media []VariantMedia` is tagged `db:"-"` today (populated separately,
same as `Product.Options`/`Media`) so it is **not** part of `model.Variant`. Keep
`Options []byte` as raw JSONB bytes in both `model.Variant` and `domain.Variant` — do
not attempt to change it to `[]VariantOption` in this task, that's a separate concern
(see `issue.md` section 2, "Persistence-format leak") and out of scope here:

```go
// internal/infrastructure/repository/model/variant_model.go
package model

type Variant struct {
	ID        uuid.UUID       `db:"id"`
	ProductID uuid.UUID       `db:"product_id"`
	SKU       *string         `db:"sku"`
	Barcode   *string         `db:"barcode"`
	Title     *string         `db:"title"`
	Price     decimal.Decimal `db:"price"`
	Weight    decimal.Decimal `db:"weight"`
	Position  int             `db:"position"`
	Stock     int             `db:"stock"`
	Options   []byte          `db:"options"`
	IsDeleted bool            `db:"is_deleted"`
	CreatedAt time.Time       `db:"created_at"`
	UpdatedAt *time.Time      `db:"updated_at"`
}

func (m Variant) ToDomain() domain.Variant { /* map every field, same shape as above */ }

type VariantMedia struct {
	VariantID uuid.UUID `db:"variant_id"`
	MediaID   uuid.UUID `db:"media_id"`
	Position  int       `db:"position"`
}

func (m VariantMedia) ToDomain() domain.VariantMedia { /* map every field */ }
```

(Add the `uuid`, `decimal`, and `time` imports as needed — same as `product_model.go`.)

**`model.InventoryLevel`** (`inventory_model.go`) — the only inventory struct that
needs a model counterpart (see "Structs in scope" above for why `InventoryItem`/
`StockMove` don't):

```go
// internal/infrastructure/repository/model/inventory_model.go
package model

type InventoryLevel struct {
	ID              uuid.UUID `db:"id"`
	InventoryItemID uuid.UUID `db:"inventory_item_id"`
	LocationID      uuid.UUID `db:"location_id"`
	AvailableQty    int       `db:"available_qty"`
	ReservedQty     int       `db:"reserved_qty"`
	UpdatedAt       time.Time `db:"updated_at"`
}

func (m InventoryLevel) ToDomain() domain.InventoryLevel { /* map every field */ }
```

### 3. Update every scan site in `productRepo.go` to use `model.*`, then convert

Grep to find every call site first, to make sure you don't miss one — this list was
correct at the time of writing this task, but re-grep before editing since line
numbers shift as you edit:

```bash
grep -n "RowToStructByName\[domain\.\|RowToStructByName\[productListRow\]\|RowToStructByName\[productDetailRow\]" internal/infrastructure/repository/productRepo.go
```

Expected matches (line numbers as of writing this task):

| Line | Current                                              | Change to                                       |
|------|-------------------------------------------------------|--------------------------------------------------|
| 310  | `RowToStructByName[productListRow]`                   | keep — `productListRow` itself changes (step 4) |
| 409  | `RowToStructByName[domain.Product]`                    | `RowToStructByName[model.Product]` + `.ToDomain()` |
| 435  | `RowToStructByName[domain.Product]`                    | same as above |
| 583  | `RowToStructByName[domain.ProductOption]`              | `RowToStructByName[model.ProductOption]` + `.ToDomain()` |
| 683  | `RowToStructByName[domain.ProductOption]`              | same |
| 709  | `RowToStructByName[domain.ProductOptionValue]`         | `RowToStructByName[model.ProductOptionValue]` + `.ToDomain()` |
| 747  | `RowToStructByName[domain.ProductOptionValue]`         | same |
| 770  | `RowToStructByName[domain.ProductOptionValue]`         | same |
| 810  | `RowToStructByName[domain.ProductOptionValue]`         | same |
| 848  | `RowToStructByName[productDetailRow]`                  | keep — `productDetailRow` itself changes (step 4) |
| 901  | `RowToStructByName[domain.ProductOption]`              | `RowToStructByName[model.ProductOption]` + `.ToDomain()` (then map `.Values` per-item) |
| 920  | `RowToStructByName[domain.ProductOptionValue]`         | `RowToStructByName[model.ProductOptionValue]` + `.ToDomain()` per-item |
| 972  | `RowToStructByName[domain.Variant]`                    | `RowToStructByName[model.Variant]` + `.ToDomain()` per-item |
| 991  | `RowToStructByName[domain.VariantMedia]`                | `RowToStructByName[model.VariantMedia]` + `.ToDomain()` per-item |
| 1024 | `RowToStructByName[domain.ProductMedia]`                | `RowToStructByName[model.ProductMedia]` + `.ToDomain()` per-item |
| 1463 | `RowToStructByName[domain.InventoryLevel]`              | `RowToStructByName[model.InventoryLevel]` + `.ToDomain()` |
| 1506 | `RowToStructByName[domain.Variant]`                     | `RowToStructByName[model.Variant]` + `.ToDomain()` |
| 1816 | `RowToStructByName[domain.ProductMedia]`                | `RowToStructByName[model.ProductMedia]` + `.ToDomain()` per-item |
| 1845 | `RowToStructByName[domain.ProductMedia]`                | `RowToStructByName[model.ProductMedia]` + `.ToDomain()` |
| 1863 | `RowToStructByName[domain.ProductMedia]`                | `RowToStructByName[model.ProductMedia]` + `.ToDomain()` |
| 1951 | `RowToStructByName[domain.ProductMedia]`                | `RowToStructByName[model.ProductMedia]` + `.ToDomain()` |
| 1977 | `RowToStructByName[domain.VariantMedia]`                | `RowToStructByName[model.VariantMedia]` + `.ToDomain()` |

For a single-row site (`pgx.CollectOneRow`), the pattern is:

```go
// before
product, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.Product])
if err != nil {
	return domain.Product{}, err
}
return product, nil

// after
row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.Product])
if err != nil {
	return domain.Product{}, err
}
return row.ToDomain(), nil
```

For a multi-row site (`pgx.CollectRows`), convert with a loop or a small helper:

```go
// before
options, err := pgx.CollectRows(optionRows, pgx.RowToStructByName[domain.ProductOption])

// after
optionModels, err := pgx.CollectRows(optionRows, pgx.RowToStructByName[model.ProductOption])
if err != nil {
	return nil, err
}
options := make([]domain.ProductOption, 0, len(optionModels))
for _, m := range optionModels {
	options = append(options, m.ToDomain())
}
```

Add the import `"gin-product-service/internal/infrastructure/repository/model"`
(check the exact module path in `go.mod` first) to `productRepo.go`.

### 4. Update `productListRow` and `productDetailRow` to embed `model.Product`

These two structs (`productRepo.go:1036` and `productRepo.go:1049`) currently embed
`domain.Product` directly, relying on its `db:"..."` tags for the base columns:

```go
type productListRow struct {
	domain.Product
	CategorySlug     *string         `db:"category_slug"`
	CategoryName     *string         `db:"category_name"`
	StartPrice       decimal.Decimal `db:"start_price"`
	MaxPrice         decimal.Decimal `db:"max_price"`
	ThumbnailType    *string         `db:"thumbnail_type"`
	ThumbnailURL     *string         `db:"thumbnail_url"`
	ThumbnailAltText *string         `db:"thumbnail_alt_text"`
}
```

Change `domain.Product` to `model.Product` in both `productListRow` and
`productDetailRow`. Then fix their call sites:

- `productRepo.go:316-333` (inside the `productListRow` loop): `p := row.Product`
  becomes `p := row.Product.ToDomain()` — everything after that (`p.Category.Slug =
  ...`, `p.Prices.StartPrice = ...`, etc.) stays the same, since it's already setting
  fields on the `domain.Product` value, not the row.
- The `productDetailRow` call site (around `productRepo.go:848` in `findProductBy`):
  same fix — call `.ToDomain()` on the embedded `model.Product` before assigning
  category slug/name onto the resulting `domain.Product`.

### 5. Strip `db:"..."` tags from the domain structs

Now remove every `db:"..."` tag (including `db:"-"`) from:
- `internal/domain/product.go`: `Product`, `ProductOption`, `ProductOptionValue`,
  `ProductMedia`
- `internal/domain/variant.go`: `Variant`, `VariantMedia`
- `internal/domain/inventory.go`: `InventoryItem`, `InventoryLevel`, `StockMove`

Keep the `json:"..."` tags exactly as they are — those are still needed for API
responses and are out of scope for this task (see `issue.md` Phase 2/3 split: Phase 2
already handled `binding`/`validate`, this task only handles `db`).

While you're in `internal/domain/inventory.go`, also delete the two dead
commented-out lines the review flagged (`// AvailableQty decimal.Decimal ...` and
`// ReservedQty decimal.Decimal ...` on `InventoryLevel`, and `// Quantity
decimal.Decimal ...` on `StockMove`) — they're commented-out code with no tags to
strip, just delete them outright. Also delete the commented-out `// Stock
decimal.Decimal` line on `domain.Variant`. Do not otherwise change these structs'
field types (e.g. don't switch `int` to `decimal.Decimal` — that's Phase 7).

### 6. Fix remaining compile errors

```bash
go build ./...
```

Fix anything still referencing a `db:"..."` tag indirectly (there shouldn't be any —
`db` tags are inert Go struct tags, removing them can't break compilation on its own;
any errors at this point come from step 3/4's `.ToDomain()` conversions being
incomplete or missing, not from tag removal itself).

### 7. Verify

```bash
go build ./...
go vet ./...
go test ./...
```

All must pass. Also confirm no domain struct in the three in-scope files still has a
`db` tag:

```bash
grep -n "db:\"" internal/domain/product.go internal/domain/variant.go internal/domain/inventory.go
```

This must return no results. (`internal/domain/category.go` will still show matches —
that's expected and out of scope.)

### 8. Manual sanity check

Start the server (`air` or `go run cmd/main.go`) and hit:
- `GET /api/v1/products` (list — exercises `productListRow`)
- `GET /api/v1/products/:id` (detail — exercises `productDetailRow`, options, variants,
  media, inventory level lookups)

Confirm the JSON response shape is byte-for-byte the same as before this refactor
(field names, nesting, computed `category`/`prices`/`thumbnail` all still populated
correctly). This is the step most likely to catch a missed field in a `ToDomain()`
mapping — a forgotten field will silently zero-value instead of failing to compile.

## Notes for whoever picks this up

- This is mechanical and low-risk *if* every `ToDomain()` method maps every field.
  The most common mistake is copy-pasting a `ToDomain()` method and forgetting to
  update one field name for a different struct — double-check each one field-by-field
  against the domain struct's definition after writing it.
- Don't add `NewX()` constructors, validation, or any business logic to the `model`
  package — it exists purely to carry `db` tags for scanning. If you find yourself
  wanting to add behavior there, stop; that's Phase 4 territory.
- Don't touch `internal/domain/category.go`, `categoryRepo.go`, or anything under
  `internal/app/category/` — category is a separate, already-shipped module and not
  part of this refactor.
- If you hit a struct with a `db:"..."` tag in these three files that this doc didn't
  mention, re-check whether it's actually scanned via `RowToStructByName` (grep for
  its name) before assuming it needs a `model` counterpart — some, like
  `InventoryItem`/`StockMove`, are write-only and just need the tag deleted.
