# `internal/domain/product.go` — DDD/SOLID Review & Refactoring Proposal

Scope: `internal/domain/product.go` (566 lines, single `package domain` file covering
Product, Option, Variant, Media, Inventory and Stock Move).

## Summary

The file conflates at least **three separate bounded contexts** (Catalog/Product,
Inventory, Stock Movement) and **three separate concerns per struct** (domain entity,
persistence row, HTTP DTO) into one flat, anemic package. This creates aggregate
boundary leakage, makes invariants unenforceable, and produces fat interfaces that
generate testing friction. Findings below are grouped by principle violated, followed
by a phased refactor proposal.

---

## 1. Domain boundary leakage

- **Persistence tags on domain entities.** Every struct carries `db:"..."` tags
  (`Product`, `Variant`, `InventoryLevel`, `StockMove`, ...). The domain layer should
  define entities free of storage concerns; the `pgx.RowToStructByName` mapping is an
  infrastructure/repository detail (per `CLAUDE.md`'s own layering: "entity structs and
  interface definitions only"). Today a schema/column rename forces a change in the
  domain package.
- **HTTP/transport tags on domain entities.** `json:"..."`, `binding:"required"`, and
  `validate:"..."` tags appear throughout (`CreateProductInput`, `VariantInput`,
  `UpdateProductInput`, `ProductOptionInput`, etc.). `binding` is a Gin-specific
  concept — this is a `delivery/http` DTO concern living in `domain`. The same struct
  is simultaneously a DB row shape, a wire DTO, and (nominally) a business entity —
  three responsibilities, one type, violating SRP three times over.
- **Read-projection shapes bolted onto the write aggregate.** `ProductCategory`,
  `ProductPrices`, and `ProductThumbnail` are query-time computed/joined shapes (per
  their own doc comments: "computed across a product's non-deleted variants",
  "sourced from the product_media row with position = 1") embedded directly as fields
  on `Product`. This blurs command/query separation: the aggregate used to *create* a
  product carries fields that only make sense on a *list/detail response*.
- **Cross-aggregate embedding instead of reference.** `Product` holds both
  `CategoryID int` (a reference) **and** `Category ProductCategory` (an embedded
  denormalized copy of another aggregate's data). A product aggregate should reference
  Category by ID only; assembling the display shape is an application/query-layer
  job, not a domain-layer field.

## 2. Loss of aggregate abstraction

- **No aggregate root behavior — pure data bags.** Every type in the file is public
  fields with zero methods. There is no `NewProduct(...)`, no `product.AddVariant(...)`,
  no `product.Archive()`. Any code anywhere can mutate `product.Variants`,
  `product.Options`, or flip `Status` directly, bypassing whatever invariants exist
  (e.g. "an archived product can't gain new active variants", "SKU must be unique
  within the product", "only one thumbnail per product"). Those rules currently only
  exist as *sentinel errors* (`ErrSKUAlreadyExists`, `ErrInvalidOption`, ...) checked
  ad hoc by use cases, not enforced by the type system.
- **Product, Inventory, and StockMove are separate bounded contexts crammed into one
  aggregate file.** `InventoryItem`, `InventoryLevel`, and `StockMove` are Odoo/Shopify
  concepts that live in their own module per `PRD.md`, yet they're declared in
  `product.go` alongside `ProductRepository`, and `ProductRepository` itself exposes
  `AdjustVariantStock`/`VariantHasHistory` — inventory-mutation operations owned by the
  Product repository. This makes "Product" a god-aggregate that reaches into another
  context's consistency boundary instead of communicating with it through an explicit
  interface (e.g. an `InventoryService`).
- **Persistence-shaped escape hatch for computed data.** `Variant.Stock int` is
  documented as "computed (not stored)... only populated on the single-product detail
  path" — a read-model value smuggled onto the write aggregate's struct, with a
  commented-out `decimal.Decimal` version left as dead code (`product.go:112`). This
  is a CQRS leak: the same `Variant` type serves both as a transactional aggregate
  member and a read-side projection depending on which code path populated it, so
  nothing in the type signals whether `Stock` is trustworthy at any given call site.
- **Persistence-format leak inside the aggregate.** `Variant.Options []byte` stores raw
  JSONB straight on the domain entity (`db:"options"`), forcing every consumer to know
  to unmarshal into `[]VariantOption` themselves. The domain should hold
  `Options []VariantOption` and let the repository handle (de)serialization.

## 3. SOLID violations

- **ISP: `ProductRepository` is a 30+ method god interface** (`product.go:327-376`)
  spanning product header CRUD, options, option values, variants, variant stock
  adjustment, variant media, product media, and variant-media linking. Every
  consumer/mock must implement all 30 methods even if it only touches media. This
  should be split along aggregate/sub-resource lines: `ProductRepository`,
  `OptionRepository`, `VariantRepository`, `MediaRepository`, `InventoryRepository`.
- **SRP at the module level: Product "owns" Option, Variant, Media, and Inventory
  operations.** `OptionUseCase`, `VariantUseCase`, `MediaUseCase` are reasonably
  scoped individually, but all their DTOs and the repository contract for all of them
  sit in one undifferentiated `product` domain concept — there's no compiler-enforced
  boundary telling a developer that variant-media logic shouldn't reach into stock
  movement internals.
- **OCP friction from primitive/stringly-typed status and enums used without
  validation helpers.** `ProductStatus`/`StockMoveType` are typed strings but have no
  `IsValid()`/`CanTransitionTo()` methods, so every layer that needs to validate a
  transition re-implements the whitelist (see `ErrProductInvalidStatus`,
  `oneof=draft active archived` repeated as a validate tag on three different structs:
  `CreateProductInput`, `UpdateProductInput`, and implicitly the DB check constraint).
  Adding a new status requires updates in N places instead of one.

## 4. Primitive obsession / missing value objects

- `AvailableQty int`, `ReservedQty int` (`InventoryLevel`), and `Quantity int`
  (`StockMove`) are raw ints with commented-out `decimal.Decimal` alternatives left in
  place (`product.go:152-153,165`) — dead code plus an open question about which type
  is authoritative. Neither the `int` nor a bare `decimal.Decimal` prevents negative
  stock; a `Quantity` value object with a constructor that rejects negative values
  would make "stock can't go negative" a compile-time-adjacent guarantee instead of a
  use-case-level check.
- `Price decimal.Decimal` / `Weight decimal.Decimal` have no unit or currency — a
  `Money` value object (amount + currency) is missing entirely; today currency is
  presumably assumed store-wide, which is an implicit, undocumented invariant.
- `SKU *string`, `Barcode *string`, `Handle string` are bare strings with uniqueness/
  format rules enforced only via DB constraints and sentinel errors
  (`ErrSKUAlreadyExists`, `ErrProductHandleAlreadyExists`), never via a value type that
  validates format at construction.

## 5. Testing friction

- **Fat `ProductRepository` interface forces 30-method mocks** for any use-case test,
  even one that only exercises option reordering. This discourages writing focused
  unit tests and pushes teams toward fewer, broader integration tests.
- **No factory/constructor functions** mean invalid aggregates are trivial to
  construct in tests (e.g. a `Product` with `Variants` referencing options that don't
  exist in `Options`), so tests can't rely on "if it compiles/constructs, it's valid" —
  every test must re-assert invariants that should have been guaranteed by the type.
- **Anemic model pushes all business-rule testing to the use-case layer**, which
  requires mocking the repository, than allowing pure, dependency-free unit tests
  against domain methods (e.g. `product.Archive()`, `variant.AdjustStock(qty)`).
  This is slower to write and slower to run at scale.
- **Framework-tag coupling.** Because `binding`/`validate` struct tags live on the same
  types used elsewhere, any test that constructs these structs is implicitly coupled
  to `go-playground/validator`/Gin conventions even when the test has nothing to do
  with HTTP binding.

---

## Refactoring Proposal

### Phase 1 — Split by bounded context (biggest leverage, lowest risk)
Break `product.go` into separate files/concepts reflecting the actual bounded
contexts already implied by `PRD.md`:
- `internal/domain/product.go` — `Product`, `ProductOption(Value)`, `ProductMedia`,
  status enum + errors, `ProductRepository` (product/option/media only).
- `internal/domain/variant.go` — `Variant`, `VariantMedia`, `VariantOption`,
  `VariantRepository`.
- `internal/domain/inventory.go` — `InventoryItem`, `InventoryLevel`, `StockMove`,
  `StockMoveType`, `InventoryRepository` (owns `AdjustVariantStock`,
  `VariantHasHistory`, stock move creation). Product/Variant use cases depend on this
  via interface, not direct field access.

### Phase 2 — Move HTTP DTOs out of `domain`
Relocate `CreateProductInput`, `UpdateProductInput`, `VariantInput`,
`CreateVariantInput`, `*MediaInput`, `PositionUpdate`, etc. (anything with `binding`/
Gin-flavored `validate` tags) to `internal/delivery/http/dto` (or similar). Handlers
map DTO → a plain application-layer command struct (no tags) before calling the use
case. This removes the Gin/validator dependency from `domain` entirely and lets
`domain` use cases accept clean, tag-free input types.

### Phase 3 — Strip persistence tags from entities
Keep `db:"..."` tags only on repository-layer row structs (introduce
`infrastructure/repository/model` structs if the shape must diverge from the domain
entity), or, if `pgx.RowToStructByName` mapping onto the domain type directly is kept
for pragmatism, at minimum stop adding `json`/`binding` alongside `db` on the same
field — one tag vocabulary per struct, one struct per responsibility.

### Phase 4 — Introduce aggregate behavior
Add constructors and mutation methods so invariants live with the data:
```go
func NewProduct(handle, title string, categoryID int) (*Product, error)
func (p *Product) AddVariant(v Variant) error   // enforces option/SKU consistency
func (p *Product) Archive() error               // enforces allowed transitions
func (l *InventoryLevel) Reserve(qty int) error // rejects qty > AvailableQty
func (s ProductStatus) CanTransitionTo(next ProductStatus) bool
```
Use cases call these instead of mutating struct fields directly, and unit tests can
exercise the rules without a mock repository.

### Phase 5 — Separate read models from the write aggregate
Move `ProductCategory`, `ProductPrices`, `ProductThumbnail`, `PaginatedProducts`,
`ListProductParams` into a query-side type (e.g. `ProductListItem`/`ProductDetail`
in a `domain` read-model file, or a dedicated `query` package) so `Product` (the
create/update aggregate) no longer carries fields that only exist for list/detail
responses.

### Phase 6 — Split `ProductRepository`
Break into `ProductRepository`, `OptionRepository`, `VariantRepository`,
`MediaRepository`, `InventoryRepository` per Phase 1's file split. Wire each
individually in `internal/wire/container.go`; use cases depend only on the
sub-interface they actually need (ISP).

### Phase 7 — Introduce value objects (lower priority, do incrementally)
- `Quantity` (non-negative int/decimal wrapper) for `AvailableQty`, `ReservedQty`,
  `StockMove.Quantity`.
- `Money` (amount + currency) for `Price`.
- Resolve the dead commented-out `decimal.Decimal` lines (`product.go:112,152-153,165`)
  one way or the other — delete or migrate, don't leave both.

Suggested order of execution: **Phase 2 and 3 first** (they're mechanical, low-risk,
and immediately un-block the "one struct, three responsibilities" problem), then
**Phase 1 and 6** (file/interface splits, still mechanical), then **Phase 4 and 5**
(behavioral changes, need test coverage first), then **Phase 7** opportunistically.
