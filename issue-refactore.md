# Task: Introduce a `Quantity` value object (Phase 7, first slice)

Source: `issue.md`, "Phase 7 — Introduce value objects (lower priority, do
incrementally)".

> Audience note: this doc is written so a junior developer or another LLM can execute
> it without re-deriving the plan. Follow the steps in order. Don't skip ahead or
> combine steps — each one is designed to leave the repo in a compilable state so
> mistakes are easy to isolate. Run `go build ./...` after every step.

## Context — what already happened

- Phase 1 (bounded-context split): `internal/domain/product.go`, `variant.go`, and
  `inventory.go` exist as separate files. `inventory.go` owns `InventoryItem`,
  `InventoryLevel`, `StockMove`, `StockMoveType`, and the sentinel errors
  `ErrInvalidQuantity` / `ErrInsufficientStock`.
- Phase 4 (aggregate behavior): `InventoryLevel` already has a `Reserve(qty int) error`
  method that rejects negative `qty` (`ErrInvalidQuantity`) and over-reservation
  (`ErrInsufficientStock`). This task changes `Reserve`'s signature, not its rules.
- Phase 6 (split `ProductRepository`): `domain.VariantRepository` (in `variant.go`)
  owns `AdjustVariantStock(ctx, variantID uuid.UUID, targetQty int) (InventoryLevel, error)`.
- `internal/domain/category.go` is a separate, already-shipped module — **out of
  scope** for this task. Do not touch `category.go` or `categoryRepo.go`.

Today, `InventoryLevel.AvailableQty`, `InventoryLevel.ReservedQty`, and
`StockMove.Quantity` are all plain `int`. Nothing in the type system stops a caller
from constructing `InventoryLevel{AvailableQty: -5}` or calling
`AdjustVariantStock(ctx, id, -5)` — the only guard today is `Reserve`'s own
`qty < 0` check, which doesn't help the many other places these fields are set
directly.

`issue.md`'s Phase 7 also lists a `Money` value object for `Price`/`Weight`, and
"resolve the dead commented-out `decimal.Decimal` lines". This task does the second
of those (the dead code turned out to still exist, just relocated by earlier phases —
see step 5) but **not** `Money` — see "What this task deliberately does NOT do" below
for why.

## Goal

Add a `Quantity` value object to `internal/domain/inventory.go`:

```go
type Quantity int
func NewQuantity(n int) (Quantity, error) // rejects n < 0
```

Use it everywhere a stock count is created, so "stock can't go negative" is enforced
at construction instead of re-checked ad hoc:

- `InventoryLevel.AvailableQty`, `InventoryLevel.ReservedQty` → `Quantity`
- `StockMove.Quantity` → `Quantity`
- `InventoryLevel.Reserve(qty int)` → `Reserve(qty Quantity)`
- `VariantRepository.AdjustVariantStock(ctx, variantID, targetQty int)` →
  `AdjustVariantStock(ctx, variantID, targetQty Quantity)`

And delete the dead code found along the way (step 5): an unused
`computeAdjustDelta` function and several commented-out `decimal.Decimal` lines in
`internal/app/product/insertUseCase.go`.

### What this task deliberately does NOT do (out of scope)

- **No `Money` value object.** `Price`/`Weight` (`decimal.Decimal`) are referenced far
  more widely than stock counts: `internal/domain/variant.go`'s `VariantInput`,
  `CreateVariantInput`, `UpdateVariantInput`; `internal/domain/product_readmodel.go`'s
  `ProductPrices`; three DTOs in `internal/delivery/http/dto/variant_dto.go` with
  `binding`/`validate` tags; and multiple response structs in
  `internal/delivery/http/handler/product_handler.go` that currently serialize
  `Price`/`Weight` as bare JSON numbers. Introducing `Money` (amount + currency) would
  either change the wire format (a breaking API change) or require a custom
  `MarshalJSON`/`UnmarshalJSON` pair plus DTO conversion at every one of those call
  sites — a materially bigger, riskier change than the mechanical `Quantity` swap
  here. Do it as its own follow-up task once someone is ready to also decide the
  wire-format question (keep `{"price": 19.99}` via custom JSON marshaling, or change
  it to `{"price": {"amount": "19.99", "currency": "USD"}}`).
- **No change to `Variant.Stock int`.** This is a separate, already-documented issue
  (`issue.md` §2: "computed (not stored)... a read-model value smuggled onto the
  write aggregate") about CQRS leakage, not about missing validation. It's a read-only
  computed projection, never constructed from user input, so a non-negative
  constructor wouldn't add any safety here. Leave it alone.
- **No change to `Price`/`Weight`/`decimal.Decimal` anywhere.**
- Do not touch `internal/domain/category.go`, `categoryRepo.go`, or anything under
  `internal/app/category/`.

### Why this is lower-risk than it sounds

- `InventoryLevel` and `StockMove` are **never serialized in an HTTP response** —
  confirmed by grepping `internal/delivery/` for both type names (no hits). So
  changing their field types can't change the API's wire format.
- `Quantity` is `type Quantity int` — a defined type with underlying kind `int`. Code
  that compares it to an untyped integer literal (`qty < 0`, `l.AvailableQty != 6`) or
  prints it with `%d` keeps compiling unchanged. Concretely, this means
  `internal/domain/inventory_test.go` (which does exactly that) **needs no edits at
  all** — read it after step 2 to confirm before moving on.
- Every negative-stock check this task adds either duplicates a check that already
  exists one call up the stack, or replaces a manual `< 0` check with an equivalent
  constructor call — no behavior changes, only where the guarantee lives.

## Step-by-step

### 1. Add the `Quantity` type to `internal/domain/inventory.go`

Add this directly below the existing `var (...)` error block (after
`ErrInsufficientStock`, before `type InventoryItem struct`):

```go
// Quantity is a non-negative count of stock units. Constructing one via
// NewQuantity is the compile-time-adjacent guarantee that "stock can't go
// negative" — callers that already validated non-negative input (e.g. rows
// read back from this app's own database) may convert directly via
// Quantity(n) instead of re-validating trusted data.
type Quantity int

// NewQuantity constructs a Quantity, rejecting negative input.
func NewQuantity(n int) (Quantity, error) {
	if n < 0 {
		return 0, ErrInvalidQuantity
	}
	return Quantity(n), nil
}

// Int returns the underlying int, for arithmetic and passing to SQL query args.
func (q Quantity) Int() int {
	return int(q)
}
```

Build after this step: `go build ./...` should still pass — nothing uses `Quantity`
yet.

### 2. Change `InventoryLevel`, `StockMove`, and `Reserve` to use `Quantity`

Still in `inventory.go`:

```go
// before
type InventoryLevel struct {
	ID              uuid.UUID `json:"id"`
	InventoryItemID uuid.UUID `json:"inventory_item_id"`
	LocationID      uuid.UUID `json:"location_id"`
	AvailableQty    int       `json:"available_qty"`
	ReservedQty     int       `json:"reserved_qty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

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

type StockMove struct {
	ID              uuid.UUID     `json:"id"`
	InventoryItemID uuid.UUID     `json:"inventory_item_id"`
	FromLocationID  *uuid.UUID    `json:"from_location_id,omitempty"`
	ToLocationID    *uuid.UUID    `json:"to_location_id,omitempty"`
	MoveType        StockMoveType `json:"move_type"`
	Quantity        int           `json:"quantity"`
	CreatedBy       *uuid.UUID    `json:"created_by,omitempty"`
	Reason          *string       `json:"reason,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
}
```

```go
// after
type InventoryLevel struct {
	ID              uuid.UUID `json:"id"`
	InventoryItemID uuid.UUID `json:"inventory_item_id"`
	LocationID      uuid.UUID `json:"location_id"`
	AvailableQty    Quantity  `json:"available_qty"`
	ReservedQty     Quantity  `json:"reserved_qty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (l *InventoryLevel) Reserve(qty Quantity) error {
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

type StockMove struct {
	ID              uuid.UUID     `json:"id"`
	InventoryItemID uuid.UUID     `json:"inventory_item_id"`
	FromLocationID  *uuid.UUID    `json:"from_location_id,omitempty"`
	ToLocationID    *uuid.UUID    `json:"to_location_id,omitempty"`
	MoveType        StockMoveType `json:"move_type"`
	Quantity        Quantity      `json:"quantity"`
	CreatedBy       *uuid.UUID    `json:"created_by,omitempty"`
	Reason          *string       `json:"reason,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
}
```

The `Reserve` body is untouched byte-for-byte except the parameter type — `qty < 0`
and `qty > l.AvailableQty` still compile because `Quantity`'s underlying kind is
`int`.

Now open `internal/domain/inventory_test.go` and confirm it still compiles as-is
(it should — do not edit it): `InventoryLevel{AvailableQty: 10, ReservedQty: 0}` and
`l.Reserve(-1)` both work because untyped constants convert to `Quantity`
automatically.

Build after this step: `go build ./internal/domain/...` should pass;
`go vet ./internal/domain/...` should pass; `go test ./internal/domain/...` should
pass unchanged. `go build ./...` as a whole will **fail** — every place elsewhere in
the repo that sets `AvailableQty`/`ReservedQty`/`Quantity` with a plain `int`, or
calls `Reserve`/`AdjustVariantStock` with one, now has a type mismatch. Steps 3-7 fix
those in order.

### 3. Update `VariantRepository.AdjustVariantStock`'s signature

Open `internal/domain/variant.go`. Change only the parameter type — the method's
position in the interface and every other method are untouched:

```go
// before
AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty int) (InventoryLevel, error)

// after
AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty Quantity) (InventoryLevel, error)
```

Build after this step: still expected to fail elsewhere — that's expected.

### 4. Update `internal/infrastructure/repository/productRepo.go`

This file has four places touching the changed fields: two build `InventoryLevel`/
`StockMove` query args (in `Create` and `CreateVariantsWithStock`), and one is
`AdjustVariantStock`'s implementation. The repository layer's row-scanning model
(`internal/infrastructure/repository/model/inventory_model.go`) keeps its fields as
plain `int` — only its `ToDomain()` conversion changes. This mirrors Phase 3's rule:
persistence-shaped types stay in `model`, domain types don't carry storage concerns.

**4a. `model/inventory_model.go`** — convert in `ToDomain()`, not in the struct:

```go
// before
func (m InventoryLevel) ToDomain() domain.InventoryLevel {
	return domain.InventoryLevel{
		ID:              m.ID,
		InventoryItemID: m.InventoryItemID,
		LocationID:      m.LocationID,
		AvailableQty:    m.AvailableQty,
		ReservedQty:     m.ReservedQty,
		UpdatedAt:       m.UpdatedAt,
	}
}

// after
func (m InventoryLevel) ToDomain() domain.InventoryLevel {
	return domain.InventoryLevel{
		ID:              m.ID,
		InventoryItemID: m.InventoryItemID,
		LocationID:      m.LocationID,
		AvailableQty:    domain.Quantity(m.AvailableQty),
		ReservedQty:     domain.Quantity(m.ReservedQty),
		UpdatedAt:       m.UpdatedAt,
	}
}
```

This is a direct cast, not `domain.NewQuantity(...)` — these values were already
validated (or defaulted to 0) before being written to the `inventory_levels` table by
this same app, so re-validating trusted reads adds nothing.

**4b. `productRepo.go`, query-arg call sites.** There are two near-identical blocks
(inside `Create`'s transaction and inside `CreateVariantsWithStock`'s transaction),
each building `stock_moves` and `inventory_levels` insert args from
`domain.StockMove`/`domain.InventoryLevel` values. In both blocks, wrap the three
changed fields with `.Int()` where they're passed as query args:

```go
// before (appears twice, once per transaction)
_, err = tx.Exec(ctx, `
	INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
	VALUES ($1, $2, $3, $4, $5, $6)`,
	pgUUID(move.ID),
	pgUUID(move.InventoryItemID),
	pgUUID(locationID),
	pgtype.UUID{}, // NULL
	string(move.MoveType),
	move.Quantity,
)
```

```go
// after
_, err = tx.Exec(ctx, `
	INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
	VALUES ($1, $2, $3, $4, $5, $6)`,
	pgUUID(move.ID),
	pgUUID(move.InventoryItemID),
	pgUUID(locationID),
	pgtype.UUID{}, // NULL
	string(move.MoveType),
	move.Quantity.Int(),
)
```

And similarly for the `inventory_levels` insert (also appears twice):

```go
// before
_, err = tx.Exec(ctx, `
	INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
	VALUES ($1, $2, $3, $4, $5)`,
	pgUUID(level.ID),
	pgUUID(level.InventoryItemID),
	pgUUID(locationID),
	level.AvailableQty,
	level.ReservedQty,
)

// after
_, err = tx.Exec(ctx, `
	INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
	VALUES ($1, $2, $3, $4, $5)`,
	pgUUID(level.ID),
	pgUUID(level.InventoryItemID),
	pgUUID(locationID),
	level.AvailableQty.Int(),
	level.ReservedQty.Int(),
)
```

**4c. `productRepo.go`, `AdjustVariantStock`.** Only the signature and the one line
that mixes `targetQty` with the plain-`int` `currentQty` change:

```go
// before
func (r *productRepo) AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty int) (domain.InventoryLevel, error) {
```

```go
// after
func (r *productRepo) AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty domain.Quantity) (domain.InventoryLevel, error) {
```

Further down, `currentQty` (scanned straight from a SQL `int` column) stays a plain
`int` — leave its declaration and the `SELECT available_qty ...`/`Scan(&currentQty)`
block untouched. Only the delta computation changes, because `targetQty` is now a
different type than `currentQty`:

```go
// before
delta := targetQty - currentQty
```

```go
// after
delta := targetQty.Int() - currentQty
```

`delta` itself **stays a plain `int`, not `Quantity`** — an ADJUST delta can be
negative (stock going down), which is exactly what `Quantity` forbids. Do not wrap
it. The two lines below that use `delta` and `targetQty` as query args need `.Int()`
on `targetQty` only (`delta` is already `int`):

```go
// before
_, err = tx.Exec(ctx, `
	INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
	VALUES ($1, $2, $3, $4, $5, $6)`,
	pgUUID(uuid.Must(uuid.NewV7())),
	pgUUID(itemID),
	pgUUID(locationID),
	pgtype.UUID{}, // NULL
	string(domain.StockMoveAdjust),
	delta,
)
...
rows, err := tx.Query(ctx, `
	INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (inventory_item_id, location_id) DO UPDATE
	SET available_qty = EXCLUDED.available_qty, updated_at = now()
	RETURNING id, inventory_item_id, location_id, available_qty, reserved_qty, COALESCE(updated_at, now()) AS updated_at`,
	pgUUID(uuid.Must(uuid.NewV7())),
	pgUUID(itemID),
	pgUUID(locationID),
	targetQty,
	0,
)
```

```go
// after
_, err = tx.Exec(ctx, `
	INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
	VALUES ($1, $2, $3, $4, $5, $6)`,
	pgUUID(uuid.Must(uuid.NewV7())),
	pgUUID(itemID),
	pgUUID(locationID),
	pgtype.UUID{}, // NULL
	string(domain.StockMoveAdjust),
	delta,
)
...
rows, err := tx.Query(ctx, `
	INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (inventory_item_id, location_id) DO UPDATE
	SET available_qty = EXCLUDED.available_qty, updated_at = now()
	RETURNING id, inventory_item_id, location_id, available_qty, reserved_qty, COALESCE(updated_at, now()) AS updated_at`,
	pgUUID(uuid.Must(uuid.NewV7())),
	pgUUID(itemID),
	pgUUID(locationID),
	targetQty.Int(),
	0,
)
```

The final `return level.ToDomain(), nil` at the end of the function needs no change —
`ToDomain()` already handles the conversion (step 4a).

Build after this step: `go build ./internal/infrastructure/...` should pass.
`go build ./...` will still fail in `internal/app/product/*.go` and its test files —
steps 5-6 fix those.

### 5. Fix `internal/app/product/insertUseCase.go` — and delete the dead code

This file has the dead code `issue.md` originally flagged in `product.go`; it moved
here during earlier phases. Fix both the `Quantity` type change and the dead code in
the same pass since they're in the same few lines.

Current state (for reference — do not leave any of the commented-out lines):

```go
trackInventory := true
if v.TrackInventory != nil {
	trackInventory = *v.TrackInventory
}
// targetQty := decimal.NewFromInt(int64(v.Stock))

if err := product.AddVariant(domain.Variant{
	...
}); err != nil {
	return domain.Product{}, err
}

inventoryItems = append(inventoryItems, domain.InventoryItem{
	ID:             inventoryItemID,
	VariantID:      &variantID,
	TrackInventory: trackInventory,
})

// ADJUST sets available_qty to an absolute target. There is no
// pre-existing inventory_levels row for a brand-new variant, so the
// "current" available_qty is 0 and the delta equals +stock.
// delta := computeAdjustDelta(decimal.Zero, targetQty)
stockMoves = append(stockMoves, domain.StockMove{
	ID:              uuid.Must(uuid.NewV7()),
	InventoryItemID: inventoryItemID,
	MoveType:        domain.StockMoveAdjust,
	// Quantity:        delta,
	Quantity: v.Stock,
})

inventoryLevels = append(inventoryLevels, domain.InventoryLevel{
	ID:              uuid.Must(uuid.NewV7()),
	InventoryItemID: inventoryItemID,
	// AvailableQty:    targetQty,
	AvailableQty: v.Stock,
	// ReservedQty:     decimal.Zero,
	ReservedQty: 0,
})
```

Replace with:

```go
trackInventory := true
if v.TrackInventory != nil {
	trackInventory = *v.TrackInventory
}

if err := product.AddVariant(domain.Variant{
	...
}); err != nil {
	return domain.Product{}, err
}

inventoryItems = append(inventoryItems, domain.InventoryItem{
	ID:             inventoryItemID,
	VariantID:      &variantID,
	TrackInventory: trackInventory,
})

stockQty, err := domain.NewQuantity(v.Stock)
if err != nil {
	return domain.Product{}, err
}

// ADJUST sets available_qty to an absolute target. There is no
// pre-existing inventory_levels row for a brand-new variant, so the
// "current" available_qty is 0 and the delta equals +stock.
stockMoves = append(stockMoves, domain.StockMove{
	ID:              uuid.Must(uuid.NewV7()),
	InventoryItemID: inventoryItemID,
	MoveType:        domain.StockMoveAdjust,
	Quantity:        stockQty,
})

inventoryLevels = append(inventoryLevels, domain.InventoryLevel{
	ID:              uuid.Must(uuid.NewV7()),
	InventoryItemID: inventoryItemID,
	AvailableQty:    stockQty,
	ReservedQty:     0,
})
```

(Don't touch the `product.AddVariant(...)` block itself — it's shown above only to
anchor where in the loop this sits. Its `Price`/`Weight` fields are out of scope.)

`AddVariant`'s own body is not part of this edit; leave it alone. `v.Stock` is
`domain.CreateVariantInput.Stock`, a plain `int` — this is exactly the case
`NewQuantity` exists for: DTO-side validation (`validate:"gte=0"` in
`internal/delivery/http/dto/variant_dto.go`) already rejects negative stock before
this code runs, but nothing at the domain layer enforced it until now.

Now delete the now-fully-unused `computeAdjustDelta` function near the bottom of the
file:

```go
// delete this entire function
// computeAdjustDelta calculates the quantity to record for an ADJUST move:
// the difference between the current available quantity and the requested
// absolute target. This is kept generic so it can be reused by the standalone
// stock-move use case.
func computeAdjustDelta(current, target decimal.Decimal) decimal.Decimal {
	return target.Sub(current)
}
```

It has no callers anywhere in the repo (confirm with
`grep -rn computeAdjustDelta` — the only other hit should be the comment you just
deleted in the previous edit).

Finally, remove the now-unused `"github.com/shopspring/decimal"` import from this
file's import block — it was only referenced by the dead code above.

Build after this step: `go build ./internal/app/product/...` will still fail (the
loop's caller signature elsewhere and `variantUseCase.go` haven't been updated yet) —
that's step 6.

### 6. Fix `internal/app/product/variantUseCase.go`

**6a. `buildVariantStockRows`.** Give it an error return and construct the `Quantity`
once via `NewQuantity`, reusing the result for both `StockMove.Quantity` and
`InventoryLevel.AvailableQty`:

```go
// before
func buildVariantStockRows(variantID uuid.UUID, trackInventory *bool, stock int) (domain.InventoryItem, domain.StockMove, domain.InventoryLevel) {
	inventoryItemID := uuid.Must(uuid.NewV7())

	track := true
	if trackInventory != nil {
		track = *trackInventory
	}

	item := domain.InventoryItem{
		ID:             inventoryItemID,
		VariantID:      &variantID,
		TrackInventory: track,
	}

	move := domain.StockMove{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: inventoryItemID,
		MoveType:        domain.StockMoveAdjust,
		Quantity:        stock,
	}

	level := domain.InventoryLevel{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: inventoryItemID,
		AvailableQty:    stock,
		ReservedQty:     0,
	}

	return item, move, level
}
```

```go
// after
func buildVariantStockRows(variantID uuid.UUID, trackInventory *bool, stock int) (domain.InventoryItem, domain.StockMove, domain.InventoryLevel, error) {
	inventoryItemID := uuid.Must(uuid.NewV7())

	track := true
	if trackInventory != nil {
		track = *trackInventory
	}

	item := domain.InventoryItem{
		ID:             inventoryItemID,
		VariantID:      &variantID,
		TrackInventory: track,
	}

	stockQty, err := domain.NewQuantity(stock)
	if err != nil {
		return domain.InventoryItem{}, domain.StockMove{}, domain.InventoryLevel{}, err
	}

	move := domain.StockMove{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: inventoryItemID,
		MoveType:        domain.StockMoveAdjust,
		Quantity:        stockQty,
	}

	level := domain.InventoryLevel{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: inventoryItemID,
		AvailableQty:    stockQty,
		ReservedQty:     0,
	}

	return item, move, level, nil
}
```

**6b. `Create`** (the single-variant use case). Update the one call site:

```go
// before
inventoryItem, stockMove, inventoryLevel := buildVariantStockRows(variantID, input.TrackInventory, input.Stock)
```

```go
// after
inventoryItem, stockMove, inventoryLevel, err := buildVariantStockRows(variantID, input.TrackInventory, input.Stock)
if err != nil {
	logger.L(ctx).Error("create variant failed", zap.Error(err), zap.String("product_id", productID.String()))
	return domain.Variant{}, err
}
```

Note `err` is being declared here with `:=` — check the surrounding function; if `err`
is already declared earlier in scope (it is, from `product, err := uc.productRepo.FindByID(...)`
a few lines up), Go still allows `:=` here because `inventoryItem`, `stockMove`, and
`inventoryLevel` are new on the left-hand side. No special handling needed.

**6c. `BulkCreate`.** Same change, inside the `for _, v := range input.Variants` loop:

```go
// before
inventoryItem, stockMove, inventoryLevel := buildVariantStockRows(variantID, v.TrackInventory, v.Stock)
params.InventoryItems = append(params.InventoryItems, inventoryItem)
params.StockMoves = append(params.StockMoves, stockMove)
params.InventoryLevels = append(params.InventoryLevels, inventoryLevel)
```

```go
// after
inventoryItem, stockMove, inventoryLevel, err := buildVariantStockRows(variantID, v.TrackInventory, v.Stock)
if err != nil {
	logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
	return nil, err
}
params.InventoryItems = append(params.InventoryItems, inventoryItem)
params.StockMoves = append(params.StockMoves, stockMove)
params.InventoryLevels = append(params.InventoryLevels, inventoryLevel)
```

Same `:=`-in-existing-scope note as 6b applies (`err` already exists in this loop
from the `optsJSON, err := ...` call a few lines up; `inventoryItem`/`stockMove`/
`inventoryLevel` are new, so `:=` is valid).

**6d. `Update` and `BulkUpdate`.** These two already guard against negative stock
manually *before* reaching `AdjustVariantStock` — leave that check as-is and just
convert the already-validated `int` at the call site with a direct cast (not
`NewQuantity` — there's no new validation to add, `Quantity` just needs a value of
the right type):

```go
// Update — before
if input.Stock != nil {
	if _, err := uc.variantRepo.AdjustVariantStock(ctx, variantID, *input.Stock); err != nil {

// Update — after
if input.Stock != nil {
	if _, err := uc.variantRepo.AdjustVariantStock(ctx, variantID, domain.Quantity(*input.Stock)); err != nil {
```

```go
// BulkUpdate — before
if item.Fields.Stock != nil {
	if _, err := uc.variantRepo.AdjustVariantStock(ctx, item.ID, *item.Fields.Stock); err != nil {

// BulkUpdate — after
if item.Fields.Stock != nil {
	if _, err := uc.variantRepo.AdjustVariantStock(ctx, item.ID, domain.Quantity(*item.Fields.Stock)); err != nil {
```

Do not touch the existing `if input.Stock != nil && *input.Stock < 0 { return ...,
domain.ErrProductInvalidInput }` / `if item.Fields.Stock != nil && *item.Fields.Stock
< 0 { return nil, domain.ErrProductInvalidInput }` checks a few lines above each of
these — they already run first and already guarantee non-negative input by the time
the cast above executes. Changing their error type or duplicating the check with
`NewQuantity` would only add risk of changing which error callers see, for no benefit.

Build after this step: `go build ./internal/app/product/...` will still fail in test
files — step 7 fixes that.

### 7. Fix the one test call site

Open `internal/app/product/queryUseCase_test.go`. `fakeProductRepo` has a field and a
method implementing `AdjustVariantStock` — both need their `int` changed to
`domain.Quantity`:

```go
// before
adjustVariantStockTargetQty int
```

```go
// after
adjustVariantStockTargetQty domain.Quantity
```

```go
// before
func (f *fakeProductRepo) AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty int) (domain.InventoryLevel, error) {
	f.adjustVariantStockVariantID = variantID
	f.adjustVariantStockTargetQty = targetQty
	if f.adjustVariantStockErr != nil {
		return domain.InventoryLevel{}, f.adjustVariantStockErr
	}
	if f.adjustVariantStockResult != (domain.InventoryLevel{}) {
		return f.adjustVariantStockResult, nil
	}
	return domain.InventoryLevel{InventoryItemID: uuid.New(), AvailableQty: targetQty}, nil
}
```

```go
// after
func (f *fakeProductRepo) AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty domain.Quantity) (domain.InventoryLevel, error) {
	f.adjustVariantStockVariantID = variantID
	f.adjustVariantStockTargetQty = targetQty
	if f.adjustVariantStockErr != nil {
		return domain.InventoryLevel{}, f.adjustVariantStockErr
	}
	if f.adjustVariantStockResult != (domain.InventoryLevel{}) {
		return f.adjustVariantStockResult, nil
	}
	return domain.InventoryLevel{InventoryItemID: uuid.New(), AvailableQty: targetQty}, nil
}
```

No other test file references `AdjustVariantStock`, `AvailableQty`, `ReservedQty`, or
constructs a `domain.StockMove{}` literal (confirmed by grepping `*_test.go` across
the repo) — `internal/domain/inventory_test.go` was already confirmed unaffected in
step 2.

Build after this step: `go build ./...` should now pass. Run `go vet ./...` too.

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
go test ./internal/infrastructure/repository/... -v
```

If anything else fails to compile that isn't covered above, grep for the changed
field/method names to find the missed call site:

```bash
grep -rn "AvailableQty\|ReservedQty" --include=*.go internal/
grep -rn "\.Quantity\b" --include=*.go internal/domain internal/app internal/infrastructure
grep -rn "AdjustVariantStock" --include=*.go internal/
```

Every remaining `int`-typed use of these should be one of the two intentionally
unchanged spots: `productRepo.go`'s local `currentQty`/`delta` in `AdjustVariantStock`
(step 4c), and `model.InventoryLevel`'s own fields (step 4a) — everything else should
now read `Quantity` or `.Int()`.

### 9. Manual sanity check

Start the server (`air` or `go run cmd/main.go`) and exercise the stock-touching
paths, confirming behavior is identical to before this task (this is a type-safety
change, not a behavior change):

- `POST /api/v1/products` with at least one variant carrying `stock > 0` — confirms
  `insertUseCase.go`'s new `NewQuantity` call doesn't reject valid input.
- `POST /api/v1/products/:id/variants` and the bulk-create endpoint, each with
  `stock > 0`.
- `PATCH /api/v1/variants/:id` with a `stock` field set — exercises
  `AdjustVariantStock` end to end (confirm the target quantity persists correctly;
  this is the path through step 4c's `delta`/`targetQty.Int()` change).
- The bulk-update variants endpoint with a `stock` field on at least one item.
- Confirm none of the above four now reject valid non-negative stock values, and that
  the existing "stock must be >= 0" validation still rejects negative ones (it does so
  at the DTO layer before reaching any of this task's code — this task adds a second,
  domain-layer guarantee behind it, not a replacement).

## Notes for whoever picks this up

- `NewQuantity` vs. a direct `Quantity(n)` cast: use `NewQuantity` (and propagate its
  error) at any boundary where the `int` came from outside the domain layer and
  hasn't already been validated in this exact call path — `insertUseCase.go` and
  `buildVariantStockRows` both qualify because their `Stock` field has no
  domain-layer check before this task. Use a direct cast only where the value is
  already known-safe: reading a persisted DB row (`ToDomain()`), or a value that's
  already been checked for negativity earlier in the same function
  (`Update`/`BulkUpdate`'s `AdjustVariantStock` calls). Don't add a second
  `NewQuantity` check right after an existing `< 0` guard — it's redundant and risks
  swapping which sentinel error a caller sees.
- Resist adding a custom `MarshalJSON`/`UnmarshalJSON` to `Quantity`. It isn't needed:
  nothing serializes `InventoryLevel`/`StockMove` today (see "Why this is lower-risk
  than it sounds" above), and adding one preemptively is exactly the kind of
  speculative flexibility this codebase's conventions ask you to avoid.
- `Money` (Phase 7's other value object, for `Price`/`Weight`) is intentionally not
  part of this task — see "What this task deliberately does NOT do". If picked up
  later, budget time to also decide the wire-format question before touching any
  code, since `Price ` is `json`-serialized today and `Money` isn't a bare number.
- This task does not touch `Variant.Stock int` (the separate CQRS read-model leak
  noted in `issue.md` §2) — that's not a value-object gap, it's a "this field
  shouldn't be on this struct at all" gap, and Phase 5 already addressed the
  equivalent problem for `Product`'s read-only fields (`ProductListItem`/
  `ProductDetail`). If it's ever revisited, model it after that phase, not this one.
