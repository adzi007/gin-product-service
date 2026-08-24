package inventory

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestStockMove_IN(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	loc := uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(loc)
	repo.addLevel(itemID, loc, 10, 0)

	uc := NewStockMoveUseCase(repo)
	move, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:    variantID,
		MoveType:     domain.StockMoveIn,
		Quantity:     5,
		ToLocationID: &loc,
	})
	if err != nil {
		t.Fatalf("IN failed: %v", err)
	}

	level, ok := repo.level(itemID, loc)
	if !ok {
		t.Fatal("level missing after IN")
	}
	if level.AvailableQty.Int() != 15 {
		t.Fatalf("IN: available = %d, want 15", level.AvailableQty.Int())
	}
	if len(repo.moves) != 1 {
		t.Fatalf("expected 1 stock move, got %d", len(repo.moves))
	}
	m := repo.moves[0]
	if m.MoveType != domain.StockMoveIn || m.Quantity.Int() != 5 {
		t.Fatalf("unexpected move: %+v", m)
	}
	if m.ToLocationID == nil || *m.ToLocationID != loc {
		t.Fatalf("IN move must reference to_location_id")
	}
	if m.FromLocationID != nil {
		t.Fatalf("IN move must have nil from_location_id")
	}
	if move.InventoryItemID != itemID {
		t.Fatalf("move inventory_item_id = %v, want %v", move.InventoryItemID, itemID)
	}
}

func TestStockMove_OUT(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	loc := uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(loc)
	repo.addLevel(itemID, loc, 10, 0)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:      variantID,
		MoveType:       domain.StockMoveOut,
		Quantity:       5,
		FromLocationID: &loc,
	})
	if err != nil {
		t.Fatalf("OUT failed: %v", err)
	}

	level, _ := repo.level(itemID, loc)
	if level.AvailableQty.Int() != 5 {
		t.Fatalf("OUT: available = %d, want 5", level.AvailableQty.Int())
	}
}

func TestStockMove_OUTInsufficient(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	loc := uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(loc)
	repo.addLevel(itemID, loc, 5, 0)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:      variantID,
		MoveType:       domain.StockMoveOut,
		Quantity:       10,
		FromLocationID: &loc,
	})
	if err != domain.ErrInsufficientStock {
		t.Fatalf("OUT insufficient: got %v, want ErrInsufficientStock", err)
	}

	// Transaction rolled back: level unchanged, no move recorded.
	level, _ := repo.level(itemID, loc)
	if level.AvailableQty.Int() != 5 {
		t.Fatalf("available changed to %d, want 5", level.AvailableQty.Int())
	}
	if len(repo.moves) != 0 {
		t.Fatalf("expected no stock move on failure, got %d", len(repo.moves))
	}
}

func TestStockMove_TRANSFER(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	locA, locB := uuid.New(), uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(locA)
	repo.addLocation(locB)
	repo.addLevel(itemID, locA, 20, 0)
	repo.addLevel(itemID, locB, 10, 0)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:      variantID,
		MoveType:       domain.StockMoveTransfer,
		Quantity:       5,
		FromLocationID: &locA,
		ToLocationID:   &locB,
	})
	if err != nil {
		t.Fatalf("TRANSFER failed: %v", err)
	}

	levelA, _ := repo.level(itemID, locA)
	levelB, _ := repo.level(itemID, locB)
	if levelA.AvailableQty.Int() != 15 {
		t.Fatalf("TRANSFER: A = %d, want 15", levelA.AvailableQty.Int())
	}
	if levelB.AvailableQty.Int() != 15 {
		t.Fatalf("TRANSFER: B = %d, want 15", levelB.AvailableQty.Int())
	}
}

func TestStockMove_TRANSFERInsufficientAtSource(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	locA, locB := uuid.New(), uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(locA)
	repo.addLocation(locB)
	repo.addLevel(itemID, locA, 3, 0)
	repo.addLevel(itemID, locB, 10, 0)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:      variantID,
		MoveType:       domain.StockMoveTransfer,
		Quantity:       5,
		FromLocationID: &locA,
		ToLocationID:   &locB,
	})
	if err != domain.ErrInsufficientStock {
		t.Fatalf("TRANSFER insufficient: got %v, want ErrInsufficientStock", err)
	}
	levelA, _ := repo.level(itemID, locA)
	levelB, _ := repo.level(itemID, locB)
	if levelA.AvailableQty.Int() != 3 || levelB.AvailableQty.Int() != 10 {
		t.Fatalf("TRANSFER failure must not change either level: A=%d B=%d", levelA.AvailableQty.Int(), levelB.AvailableQty.Int())
	}
	if len(repo.moves) != 0 {
		t.Fatalf("expected no stock move on failure")
	}
}

func TestStockMove_ADJUSTIncrease(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	loc := uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(loc)
	repo.addLevel(itemID, loc, 10, 0)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:    variantID,
		MoveType:     domain.StockMoveAdjust,
		Quantity:     15,
		ToLocationID: &loc,
	})
	if err != nil {
		t.Fatalf("ADJUST increase failed: %v", err)
	}
	level, _ := repo.level(itemID, loc)
	if level.AvailableQty.Int() != 15 {
		t.Fatalf("ADJUST increase: available = %d, want 15", level.AvailableQty.Int())
	}
	if len(repo.moves) != 1 || repo.moves[0].Quantity.Int() != 15 {
		t.Fatalf("ADJUST move must retain requested quantity 15")
	}
}

func TestStockMove_ADJUSTDecrease(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	loc := uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(loc)
	repo.addLevel(itemID, loc, 10, 0)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:    variantID,
		MoveType:     domain.StockMoveAdjust,
		Quantity:     5,
		ToLocationID: &loc,
	})
	if err != nil {
		t.Fatalf("ADJUST decrease failed: %v", err)
	}
	level, _ := repo.level(itemID, loc)
	if level.AvailableQty.Int() != 5 {
		t.Fatalf("ADJUST decrease: available = %d, want 5", level.AvailableQty.Int())
	}
}

func TestStockMove_Validation(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	locA, locB := uuid.New(), uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(locA)
	repo.addLocation(locB)

	uc := NewStockMoveUseCase(repo)

	cases := []struct {
		name  string
		input domain.CreateStockMoveInput
		want  error
	}{
		{"missing variant", domain.CreateStockMoveInput{MoveType: domain.StockMoveIn, Quantity: 1, ToLocationID: &locA}, domain.ErrInvalidStockMoveInput},
		{"zero quantity", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveIn, Quantity: 0, ToLocationID: &locA}, domain.ErrInvalidStockMoveInput},
		{"negative quantity", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveIn, Quantity: -1, ToLocationID: &locA}, domain.ErrInvalidStockMoveInput},
		{"IN missing to_location", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveIn, Quantity: 1}, domain.ErrInvalidStockMoveInput},
		{"OUT missing from_location", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveOut, Quantity: 1}, domain.ErrInvalidStockMoveInput},
		{"TRANSFER missing to_location", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveTransfer, Quantity: 1, FromLocationID: &locA}, domain.ErrInvalidStockMoveInput},
		{"TRANSFER same location", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveTransfer, Quantity: 1, FromLocationID: &locA, ToLocationID: &locA}, domain.ErrFromToLocationSame},
		{"ADJUST missing to_location", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveAdjust, Quantity: 1}, domain.ErrInvalidStockMoveInput},
		{"unsupported move type", domain.CreateStockMoveInput{VariantID: variantID, MoveType: domain.StockMoveReserve, Quantity: 1, ToLocationID: &locA}, domain.ErrInvalidStockMoveInput},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := uc.Create(context.Background(), tc.input)
			if err != tc.want {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestStockMove_VariantNotFound(t *testing.T) {
	repo := newFakeInventoryRepo()
	loc := uuid.New()
	repo.addLocation(loc)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:    uuid.New(),
		MoveType:     domain.StockMoveIn,
		Quantity:     1,
		ToLocationID: &loc,
	})
	if err != domain.ErrVariantNotFound {
		t.Fatalf("got %v, want ErrVariantNotFound", err)
	}
}

func TestStockMove_LocationNotFound(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID, itemID := uuid.New(), uuid.New()
	repo.addVariant(variantID, itemID)

	uc := NewStockMoveUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateStockMoveInput{
		VariantID:    variantID,
		MoveType:     domain.StockMoveIn,
		Quantity:     1,
		ToLocationID: &[]uuid.UUID{uuid.New()}[0],
	})
	if err != domain.ErrLocationNotFound {
		t.Fatalf("got %v, want ErrLocationNotFound", err)
	}
}
