package product

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func strPtr(s string) *string {
	return &s
}

func decPtr(f float64) *decimal.Decimal {
	d := decimal.NewFromFloat(f)
	return &d
}

func TestVariantUseCase_Create_GeneratesIDAndPosition(t *testing.T) {
	productID := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{
			Product: domain.Product{
				ID: productID,
				Variants: []domain.Variant{
					{ID: uuid.New(), Position: 0},
					{ID: uuid.New(), Position: 1},
				},
			},
		},
	}
	uc := NewVariantUseCase(repo)

	created, err := uc.Create(context.Background(), productID, domain.CreateVariantInput{
		SKU:     strPtr("SKU-1"),
		Price:   decPtr(19.99),
		Weight:  decPtr(0.5),
		Options: map[string]string{},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("expected generated variant ID")
	}
	if created.ProductID != productID {
		t.Errorf("expected product id %s, got %s", productID, created.ProductID)
	}
	if created.Position != 2 {
		t.Errorf("expected position 2, got %d", created.Position)
	}
	if len(repo.createVariantsWithStockParams.Variants) != 1 {
		t.Fatalf("repo did not receive 1 variant, got %d", len(repo.createVariantsWithStockParams.Variants))
	}
	if repo.createVariantsWithStockParams.Variants[0].ID != created.ID {
		t.Error("repo did not receive the created variant")
	}
	if len(repo.createVariantsWithStockParams.StockMoves) != 1 ||
		repo.createVariantsWithStockParams.StockMoves[0].MoveType != domain.StockMoveAdjust {
		t.Error("expected a single ADJUST stock move for the created variant")
	}
}

func TestVariantUseCase_Create_ProductNotFound(t *testing.T) {
	repo := &fakeProductRepo{findByIDErr: domain.ErrProductNotFound}
	uc := NewVariantUseCase(repo)

	_, err := uc.Create(context.Background(), uuid.New(), domain.CreateVariantInput{
		Price:  decPtr(10),
		Weight: decPtr(1),
	})
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestVariantUseCase_Create_InvalidOption(t *testing.T) {
	productID := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{
			Product: domain.Product{
				ID: productID,
				Options: []domain.ProductOption{
					{ID: uuid.New(), Name: "Color", Values: []domain.ProductOptionValue{{ID: uuid.New(), Value: "Black"}}},
				},
			},
		},
	}
	uc := NewVariantUseCase(repo)

	_, err := uc.Create(context.Background(), productID, domain.CreateVariantInput{
		Price:   decPtr(10),
		Weight:  decPtr(1),
		Options: map[string]string{"Size": "M"}, // undeclared option
	})
	if err != domain.ErrInvalidOption {
		t.Fatalf("expected ErrInvalidOption, got %v", err)
	}
}

func TestVariantUseCase_BulkCreate_PositionsSequential(t *testing.T) {
	productID := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{
			Product: domain.Product{
				ID:       productID,
				Variants: []domain.Variant{{ID: uuid.New(), Position: 0}},
			},
		},
	}
	uc := NewVariantUseCase(repo)

	created, err := uc.BulkCreate(context.Background(), productID, domain.BulkCreateVariantsInput{
		Variants: []domain.CreateVariantInput{
			{Price: decPtr(10), Weight: decPtr(1), Options: map[string]string{}},
			{Price: decPtr(20), Weight: decPtr(2), Options: map[string]string{}},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(created))
	}
	if created[0].Position != 1 || created[1].Position != 2 {
		t.Errorf("expected positions 1 and 2, got %d and %d", created[0].Position, created[1].Position)
	}
	if len(repo.createVariantsWithStockParams.Variants) != 2 {
		t.Errorf("repo did not receive 2 variants, got %d", len(repo.createVariantsWithStockParams.Variants))
	}
	if len(repo.createVariantsWithStockParams.InventoryItems) != 2 {
		t.Errorf("repo did not receive 2 inventory items, got %d", len(repo.createVariantsWithStockParams.InventoryItems))
	}
}

func TestVariantUseCase_Update_DelegatesToRepo(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewVariantUseCase(repo)

	id := uuid.New()
	price := decPtr(29.99)
	got, err := uc.Update(context.Background(), id, domain.UpdateVariantInput{Price: price})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.ID != id {
		t.Errorf("expected variant id %s, got %s", id, got.ID)
	}
	if repo.updateVariantID != id {
		t.Errorf("expected id %s, got %s", id, repo.updateVariantID)
	}
	if repo.updateVariantInput.Price == nil || !repo.updateVariantInput.Price.Equal(*price) {
		t.Errorf("expected price %s passed through, got %v", price, repo.updateVariantInput.Price)
	}
}

func TestVariantUseCase_Delete_SoftWhenHasHistory(t *testing.T) {
	repo := &fakeProductRepo{variantHasHistoryResult: true}
	uc := NewVariantUseCase(repo)

	id := uuid.New()
	if err := uc.Delete(context.Background(), id); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.variantHasHistoryID != id {
		t.Errorf("expected history check for id %s, got %s", id, repo.variantHasHistoryID)
	}
	if repo.deleteVariantHard {
		t.Error("expected soft delete when variant has history")
	}
	if repo.deleteVariantID != id {
		t.Errorf("expected delete for id %s, got %s", id, repo.deleteVariantID)
	}
}

func TestVariantUseCase_Delete_HardWhenNoHistory(t *testing.T) {
	repo := &fakeProductRepo{variantHasHistoryResult: false}
	uc := NewVariantUseCase(repo)

	id := uuid.New()
	if err := uc.Delete(context.Background(), id); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !repo.deleteVariantHard {
		t.Error("expected hard delete when variant has no history")
	}
}

func TestVariantUseCase_Restore(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewVariantUseCase(repo)

	id := uuid.New()
	got, err := uc.Restore(context.Background(), id)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.ID != id {
		t.Errorf("expected variant id %s, got %s", id, got.ID)
	}
	if repo.restoreVariantID != id {
		t.Errorf("expected restore for id %s, got %s", id, repo.restoreVariantID)
	}
}

func TestVariantUseCase_Reorder(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewVariantUseCase(repo)

	productID := uuid.New()
	positions := []domain.PositionUpdate{{ID: uuid.New(), Position: 0}, {ID: uuid.New(), Position: 1}}
	if err := uc.Reorder(context.Background(), productID, positions); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.reorderVariantsProductID != productID {
		t.Errorf("expected product id %s, got %s", productID, repo.reorderVariantsProductID)
	}
	if len(repo.reorderVariantsPositions) != 2 {
		t.Errorf("expected 2 positions, got %d", len(repo.reorderVariantsPositions))
	}
}
