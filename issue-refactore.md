# Task: Introduce aggregate behavior on Product (Phase 4)

Source: `issue.md`, "Phase 4 — Introduce aggregate behavior".

> Audience note: this doc is written so a junior developer or another LLM can execute
> it without re-deriving the plan. Follow the steps in order. Don't skip ahead or
> combine steps — each one is designed to leave the repo in a compilable state so
> mistakes are easy to isolate. Run `go build ./...` after every step.

## Context — what already happened

- Phase 1 (bounded-context split) is done: `internal/domain/product.go`,
  `internal/domain/variant.go`, and `internal/domain/inventory.go` exist as separate
  files.
- Phase 2 (move HTTP DTOs out of `domain`) is done: no `binding:`/`validate:` tags
  remain in `product.go`/`variant.go`.
- Phase 3 (strip persistence tags) is done: no `db:"..."` tags remain in
  `product.go`, `variant.go`, or `inventory.go`. Row-scanning structs live in
  `internal/infrastructure/repository/model`.
- `internal/domain/category.go` is a separate, already-shipped module — **out of
  scope** for this task. Do not touch `category.go` or `categoryRepo.go`.

## Goal

Today every struct in `product.go`/`variant.go`/`inventory.go` is a pure data bag —
zero methods. Business rules ("archived can't go back to draft directly", "SKU must
be unique within a product") are re-implemented ad hoc inside use cases
(`internal/app/product/*.go`) by reading/writing struct fields directly. This task adds
a small, deliberately narrow set of constructors and mutation methods to the domain
types so those rules live with the data and can be unit-tested without a mock
repository, per `issue.md` Phase 4:

```go
func NewProduct(handle, title string, categoryID int) (*Product, error)
func (p *Product) AddVariant(v Variant) error   // enforces SKU uniqueness within the product
func (p *Product) Archive() error               // enforces allowed status transitions
func (p *Product) Restore() error               // enforces allowed status transitions
func (s ProductStatus) CanTransitionTo(next ProductStatus) bool
func (l *InventoryLevel) Reserve(qty int) error // rejects qty > AvailableQty or qty < 0
```

By the end of this task:
- The five methods/functions above exist on their respective types with unit tests.
- `updateProductUc.Archive`/`Restore` (in `internal/app/product/updateUseCase.go`) call
  `product.Archive()`/`product.Restore()` instead of unconditionally calling
  `productRepo.UpdateStatus`.
- `insertProductUc.Create` (in `internal/app/product/insertUseCase.go`) calls
  `domain.NewProduct(...)` and `product.AddVariant(...)` instead of hand-building the
  `domain.Product{...}` struct literal and appending to a local `variants` slice.
- `InventoryLevel.Reserve` is added with a unit test but **no call site** — there is no
  reservation feature wired up yet (only `domain.StockMoveReserve`/`StockMoveUnreserve`
  constants exist, unused). Wiring it into a real reserve/unreserve flow is future work,
  not part of this task.

This task does NOT include: splitting `ProductRepository` (Phase 6), separating read
models like `ProductCategory`/`PaginatedProducts` (Phase 5), value objects like
`Quantity`/`Money` (Phase 7), or adding aggregate methods to `Variant`,
`ProductOption`, or `VariantMedia` — those are out of scope. Do not touch
`internal/domain/category.go`, `categoryRepo.go`, or anything under
`internal/app/category/`.

## Step-by-step

### 1. Add the new error

Open `internal/domain/product.go`. In the `var (...)` error block (currently lines
92-135), add one new sentinel error next to `ErrProductInvalidStatus`:

```go
// ErrProductInvalidStatusTransition is returned when a status change is not
// allowed from the product's current status (e.g. archived -> draft).
ErrProductInvalidStatusTransition = errors.New("invalid product status transition")
```

Open `internal/domain/inventory.go` and add two new errors after the `StockMoveType`
const block:

```go
var (
	// ErrInvalidQuantity is returned when a negative quantity is passed to an
	// inventory-mutating method.
	ErrInvalidQuantity = errors.New("quantity must not be negative")
	// ErrInsufficientStock is returned when a reservation would exceed the
	// currently available quantity.
	ErrInsufficientStock = errors.New("insufficient available stock")
)
```

You'll need to add `"errors"` to `inventory.go`'s import block (it currently only
imports `"time"` and `"github.com/google/uuid"`).

Build after this step: `go build ./...` must still pass (new unused errors are fine in
Go, they're package-level vars).

### 2. Add `ProductStatus.CanTransitionTo`

In `internal/domain/product.go`, directly below the `ProductStatus` const block
(after line 19), add:

```go
// CanTransitionTo reports whether transitioning from s to next is allowed.
// Allowed transitions: draft -> active, draft -> archived, active -> archived,
// archived -> active (restore). All other transitions, including transitioning
// to the same status, are rejected.
func (s ProductStatus) CanTransitionTo(next ProductStatus) bool {
	switch s {
	case ProductStatusDraft:
		return next == ProductStatusActive || next == ProductStatusArchived
	case ProductStatusActive:
		return next == ProductStatusArchived
	case ProductStatusArchived:
		return next == ProductStatusActive
	default:
		return false
	}
}
```

This is the single source of truth for valid transitions — do not duplicate this
switch anywhere else.

### 3. Add `Product.Archive()` and `Product.Restore()`

Directly below the `Product` struct definition in `internal/domain/product.go`
(after the closing brace, before `ProductCategory`), add:

```go
// Archive transitions the product to the archived status. It mutates p in
// place and returns ErrProductInvalidStatusTransition if the current status
// cannot transition to archived.
func (p *Product) Archive() error {
	if !p.Status.CanTransitionTo(ProductStatusArchived) {
		return ErrProductInvalidStatusTransition
	}
	p.Status = ProductStatusArchived
	return nil
}

// Restore transitions an archived product back to active. It mutates p in
// place and returns ErrProductInvalidStatusTransition if the current status
// cannot transition to active.
func (p *Product) Restore() error {
	if !p.Status.CanTransitionTo(ProductStatusActive) {
		return ErrProductInvalidStatusTransition
	}
	p.Status = ProductStatusActive
	return nil
}
```

These only mutate the in-memory value — they do not talk to the repository. Wiring
into the use case is step 6.

### 4. Add `NewProduct`

Still in `internal/domain/product.go`, add this near the bottom of the file (or
directly after the `Product` struct + its new methods):

```go
// NewProduct constructs a new draft product with a generated ID, validating the
// minimal set of fields required for any product to exist. Callers set
// Description/Vendor/Options/Media directly on the returned value, and use
// AddVariant to attach variants.
func NewProduct(handle, title string, categoryID int) (*Product, error) {
	if strings.TrimSpace(handle) == "" || strings.TrimSpace(title) == "" {
		return nil, ErrProductInvalidInput
	}
	if categoryID <= 0 {
		return nil, ErrProductInvalidInput
	}
	return &Product{
		ID:         uuid.Must(uuid.NewV7()),
		Handle:     handle,
		Title:      title,
		Status:     ProductStatusDraft,
		CategoryID: categoryID,
	}, nil
}
```

Add `"strings"` to `product.go`'s import block (it currently imports `"context"`,
`"errors"`, `"time"`, `github.com/google/uuid`, `github.com/shopspring/decimal`).

### 5. Add `Product.AddVariant`

Directly below `NewProduct`, add:

```go
// AddVariant appends v to the product's variant list, enforcing that its SKU
// (if set) does not duplicate an existing variant's SKU on this product. Empty
// SKUs are not checked for uniqueness — multiple variants may have no SKU.
func (p *Product) AddVariant(v Variant) error {
	if v.SKU != nil && strings.TrimSpace(*v.SKU) != "" {
		for _, existing := range p.Variants {
			if existing.SKU != nil && *existing.SKU == *v.SKU {
				return ErrSKUAlreadyExists
			}
		}
	}
	p.Variants = append(p.Variants, v)
	return nil
}
```

Build after steps 2-5: `go build ./...` must pass — nothing calls these yet, but they
must compile standalone.

### 6. Add `InventoryLevel.Reserve`

In `internal/domain/inventory.go`, directly below the `InventoryLevel` struct
definition, add:

```go
// Reserve moves qty units from AvailableQty to ReservedQty. It returns
// ErrInvalidQuantity if qty is negative, or ErrInsufficientStock if qty
// exceeds the currently available quantity. On success it mutates l in place.
func (l *InventoryLevel) Reserve(qty int) error {
	if qty < 0 {
		return ErrInvalidQuantity
	}
	if qty > l.AvailableQty {
		return ErrInsufficientStock
	}
	l.AvailableQty -= qty
	l.ReservedQty += qty
	return nil
}
```

There is intentionally no caller for this method yet (see "Goal" above) — do not
invent a call site or force it into `AdjustVariantStock`, which is a different
operation (sets an absolute target quantity, not a reservation).

### 7. Wire `Archive`/`Restore` into the use case

Open `internal/app/product/updateUseCase.go`. Replace both methods:

```go
func (uc *updateProductUc) Archive(ctx context.Context, id uuid.UUID) error {

	product, err := uc.productRepo.FindByID(ctx, id)
	if err != nil {
		logger.L(ctx).Error("archive product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	if err := product.Archive(); err != nil {
		logger.L(ctx).Error("archive product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	if err := uc.productRepo.UpdateStatus(ctx, id, product.Status); err != nil {
		logger.L(ctx).Error("archive product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success archive product", zap.String("id", id.String()))

	return nil
}

func (uc *updateProductUc) Restore(ctx context.Context, id uuid.UUID) error {

	product, err := uc.productRepo.FindByID(ctx, id)
	if err != nil {
		logger.L(ctx).Error("restore product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	if err := product.Restore(); err != nil {
		logger.L(ctx).Error("restore product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	if err := uc.productRepo.UpdateStatus(ctx, id, product.Status); err != nil {
		logger.L(ctx).Error("restore product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success restore product", zap.String("id", id.String()))

	return nil
}
```

`domain.ProductRepository` already has `FindByID` — no interface change needed.

Note the behavior change this introduces: calling Archive on an already-archived
product (or Restore on a non-archived product) now returns
`ErrProductInvalidStatusTransition` instead of silently succeeding. Check
`internal/delivery/http` for the handlers calling `Archive`/`Restore` and confirm they
propagate use-case errors to an HTTP status the same way other domain errors do (grep
for `ErrProductNotFound` or `ErrProductInvalidStatus` in the handler package to see the
existing error-to-status mapping pattern, and add a case for the new error following
the same pattern — likely 409 Conflict, matching how `ErrSKUAlreadyExists` or similar
conflicts are mapped).

`internal/app/product/lifecycleUseCase_test.go` already has tests for
Archive/Restore — run it and update any test that asserted the old "always succeeds"
behavior, or that mocks `productRepo` without stubbing `FindByID` for these paths.

### 8. Wire `NewProduct` and `AddVariant` into `insertUseCase.go`

Open `internal/app/product/insertUseCase.go`. This is the trickiest step — go slowly.

**8a.** In `validateCreateInput`, delete the `seenSKUs` duplicate-SKU check (it becomes
redundant with `AddVariant`), but keep the rest of the per-variant validation
(Price/Weight nil check, Stock negative check):

```go
// before
seenSKUs := make(map[string]struct{}, len(input.Variants))
for _, v := range input.Variants {
	if v.Price == nil || v.Weight == nil {
		return domain.ErrProductInvalidInput
	}
	if v.Stock < 0 {
		return domain.ErrProductInvalidInput
	}
	if v.SKU != nil && strings.TrimSpace(*v.SKU) != "" {
		if _, dup := seenSKUs[*v.SKU]; dup {
			return domain.ErrSKUAlreadyExists
		}
		seenSKUs[*v.SKU] = struct{}{}
	}
}

// after
for _, v := range input.Variants {
	if v.Price == nil || v.Weight == nil {
		return domain.ErrProductInvalidInput
	}
	if v.Stock < 0 {
		return domain.ErrProductInvalidInput
	}
}
```

Also delete the handle/title/categoryID checks at the top of `validateCreateInput`
(they become redundant with `NewProduct`, added next):

```go
// delete these two blocks — NewProduct now enforces them
if strings.TrimSpace(input.Handle) == "" || strings.TrimSpace(input.Title) == "" {
	return domain.ErrProductInvalidInput
}
if input.CategoryID <= 0 {
	return domain.ErrProductInvalidInput
}
```

Leave the option-name checks and status-validity check in `validateCreateInput`
untouched.

**8b.** In `Create`, replace the `productID := uuid.Must(uuid.NewV7())` line (currently
right after the `validateCreateInput` call) with a call to `domain.NewProduct`:

```go
// before
if err := validateCreateInput(input); err != nil {
	return domain.Product{}, err
}

productID := uuid.Must(uuid.NewV7())

// after
if err := validateCreateInput(input); err != nil {
	return domain.Product{}, err
}

product, err := domain.NewProduct(input.Handle, input.Title, input.CategoryID)
if err != nil {
	return domain.Product{}, err
}
productID := product.ID
```

Everything below this (the options loop, media loop) already uses the local
`productID` variable — no further change needed there.

**8c.** In the variant-building loop, replace the final `variants = append(variants,
domain.Variant{...})` with `product.AddVariant(...)`, and delete the now-unused
`variants` slice declaration:

```go
// delete this line near the top of Create, alongside inventoryItems/stockMoves/inventoryLevels:
variants := make([]domain.Variant, 0, len(input.Variants))

// inside the `for _, v := range input.Variants` loop, replace:
variants = append(variants, domain.Variant{
	ID:        variantID,
	ProductID: productID,
	SKU:       v.SKU,
	Barcode:   v.Barcode,
	Title:     v.Title,
	Price:     *v.Price,
	Weight:    *v.Weight,
	Options:   optsJSON,
	IsDeleted: false,
	Media:     variantMedia,
})

// with:
if err := product.AddVariant(domain.Variant{
	ID:        variantID,
	ProductID: productID,
	SKU:       v.SKU,
	Barcode:   v.Barcode,
	Title:     v.Title,
	Price:     *v.Price,
	Weight:    *v.Weight,
	Options:   optsJSON,
	IsDeleted: false,
	Media:     variantMedia,
}); err != nil {
	return domain.Product{}, err
}
```

**8d.** After the loop, replace the manual `domain.Product{...}` struct literal with
setting the remaining fields directly on `product`:

```go
// before
status := domain.ProductStatusDraft
if input.Status != nil {
	status = *input.Status
}

product := domain.Product{
	ID:          productID,
	Handle:      input.Handle,
	Title:       input.Title,
	Status:      status,
	Description: input.Description,
	Vendor:      input.Vendor,
	CategoryID:  input.CategoryID,
	Options:     options,
	Variants:    variants,
	Media:       media,
}

created, err := uc.productRepo.Create(ctx, domain.CreateProductParams{
	Product:         product,
	...

// after
if input.Status != nil {
	product.Status = *input.Status
}
product.Description = input.Description
product.Vendor = input.Vendor
product.Options = options
product.Media = media

created, err := uc.productRepo.Create(ctx, domain.CreateProductParams{
	Product:         *product,
	...
```

Note `CreateProductParams.Product` is `domain.Product` (a value, not a pointer) — you
must dereference `product` (`*product`) when building the params struct, since
`NewProduct` returns `*Product`.

Also double-check: the existing `err` variable name from
`domain.NewProduct(...)` in step 8b is reused by `created, err :=
uc.productRepo.Create(...)` later — since that line uses `:=` with a new variable
(`created`) alongside `err`, this is fine in Go, but if you get a "no new variables on
left side of :=" compile error anywhere, change that specific line's `err` reuse to
match whatever the compiler flags.

### 9. Build and fix compile errors

```bash
go build ./...
```

Fix anything the compiler flags — most likely a leftover reference to the deleted
`variants` local variable, or a mismatched `Product` vs `*Product` type at a call site
in `insertUseCase.go`.

### 10. Write unit tests

Create `internal/domain/product_test.go`:

```go
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert" // check go.mod for the actual assertion lib used elsewhere in the repo; internal/app/product/*_test.go already has examples to copy the import/style from
)

func TestNewProduct(t *testing.T) {
	p, err := NewProduct("my-handle", "My Title", 1)
	assert.NoError(t, err)
	assert.Equal(t, ProductStatusDraft, p.Status)
	assert.NotEqual(t, [16]byte{}, [16]byte(p.ID))

	_, err = NewProduct("", "My Title", 1)
	assert.ErrorIs(t, err, ErrProductInvalidInput)

	_, err = NewProduct("my-handle", "My Title", 0)
	assert.ErrorIs(t, err, ErrProductInvalidInput)
}

func TestProductAddVariant(t *testing.T) {
	p, _ := NewProduct("h", "t", 1)
	sku := "SKU-1"

	assert.NoError(t, p.AddVariant(Variant{SKU: &sku}))
	assert.Len(t, p.Variants, 1)

	err := p.AddVariant(Variant{SKU: &sku})
	assert.ErrorIs(t, err, ErrSKUAlreadyExists)
	assert.Len(t, p.Variants, 1) // rejected variant must not be appended

	// nil/empty SKUs never collide with each other
	assert.NoError(t, p.AddVariant(Variant{}))
	assert.NoError(t, p.AddVariant(Variant{}))
}

func TestProductStatusCanTransitionTo(t *testing.T) {
	cases := []struct {
		from, to ProductStatus
		want     bool
	}{
		{ProductStatusDraft, ProductStatusActive, true},
		{ProductStatusDraft, ProductStatusArchived, true},
		{ProductStatusActive, ProductStatusArchived, true},
		{ProductStatusArchived, ProductStatusActive, true},
		{ProductStatusActive, ProductStatusDraft, false},
		{ProductStatusArchived, ProductStatusDraft, false},
		{ProductStatusDraft, ProductStatusDraft, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.from.CanTransitionTo(c.to), "%s -> %s", c.from, c.to)
	}
}

func TestProductArchiveRestore(t *testing.T) {
	p, _ := NewProduct("h", "t", 1)

	assert.NoError(t, p.Archive())
	assert.Equal(t, ProductStatusArchived, p.Status)

	assert.ErrorIs(t, p.Archive(), ErrProductInvalidStatusTransition)

	assert.NoError(t, p.Restore())
	assert.Equal(t, ProductStatusActive, p.Status)

	assert.ErrorIs(t, p.Restore(), ErrProductInvalidStatusTransition)
}
```

Create `internal/domain/inventory_test.go`:

```go
package domain

import "testing"

func TestInventoryLevelReserve(t *testing.T) {
	l := InventoryLevel{AvailableQty: 10, ReservedQty: 0}

	if err := l.Reserve(4); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.AvailableQty != 6 || l.ReservedQty != 4 {
		t.Fatalf("got AvailableQty=%d ReservedQty=%d, want 6/4", l.AvailableQty, l.ReservedQty)
	}

	if err := l.Reserve(100); err != ErrInsufficientStock {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}
	if err := l.Reserve(-1); err != ErrInvalidQuantity {
		t.Fatalf("got %v, want ErrInvalidQuantity", err)
	}
}
```

Before writing these, check `go.mod` and an existing test file (e.g.
`internal/app/product/lifecycleUseCase_test.go`) to see which assertion library the
repo actually uses (`testify` vs stdlib) and match that style instead of guessing —
the `product_test.go` example above uses `testify/assert`; if the repo doesn't already
depend on it, use plain stdlib `t.Fatalf`/`if got != want` checks like the
`inventory_test.go` example instead, to avoid adding a new dependency for this task.

### 11. Verify

```bash
go build ./...
go vet ./...
go test ./...
```

All must pass. Then also run:

```bash
go test ./internal/domain/... -v
go test ./internal/app/product/... -v
```

and read through the `product` package's test output specifically — `insertUseCase.go`
and `updateUseCase.go` changed behavior in ways existing tests may assert against (e.g.
a test that called `Archive` twice expecting no error will now need to expect
`ErrProductInvalidStatusTransition` on the second call).

### 12. Manual sanity check

Start the server (`air` or `go run cmd/main.go`) and exercise:
- `POST /api/v1/products` with a body containing two variants that share the same SKU
  — must now fail with the SKU-conflict error (same error, same HTTP status as before;
  only the code path that produces it changed).
- `POST /api/v1/products/:id/archive` (or whatever the actual route is — check
  `internal/delivery/http/router.go`) called twice in a row — the second call must
  return a clear error instead of silently succeeding.
- `POST /api/v1/products/:id/restore` on a non-archived product — must return a clear
  error.
- A normal create → archive → restore happy path — must behave exactly as before.

## Notes for whoever picks this up

- Resist the urge to also refactor `variantUseCase.go`'s standalone `Create`/
  `BulkCreate` (adding variants to an *existing* product, not at product-creation
  time) to use `Product.AddVariant`. Doing that correctly requires loading the full
  product with its current variants first (an extra repository round trip that doesn't
  happen today), which is a behavior/performance change, not a mechanical one. Leave it
  as-is; it can be tackled as its own follow-up once this phase is verified stable.
- Don't wire `InventoryLevel.Reserve` into `AdjustVariantStock` or any existing
  endpoint. `AdjustVariantStock` sets an absolute target quantity (used by variant
  create/update to set initial/new stock); `Reserve` models moving already-available
  stock into a reserved state for an order, which isn't a feature this codebase has an
  endpoint for yet. Adding one is out of scope.
- Don't add more transitions to `CanTransitionTo` than the five listed in step 2 unless
  a real product requirement calls for it (e.g. "active -> draft" was deliberately left
  out — there was no existing code path that did this before this task, so allowing it
  now would be scope creep, not a bug fix).
- If you find another place in `internal/app/product/` that duplicates the SKU-dup or
  status-transition logic this task centralizes, it's fine to point it at the new
  domain method in this same PR — but don't go looking for more than what steps 7-8
  already covered; a broader sweep is Phase 6/7 territory.
- Keep `NewProduct`/`AddVariant`/`Archive`/`Restore`/`Reserve` free of any repository or
  context.Context dependency — they must stay pure, dependency-free functions/methods
  so they stay trivially unit-testable. If a rule you're asked to add needs to check
  something only the database knows (e.g. "handle is unique store-wide"), that check
  stays at the use-case/repository layer, not in these methods.
