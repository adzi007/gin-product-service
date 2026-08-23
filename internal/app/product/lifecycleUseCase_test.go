package product

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestUpdateProductUseCase_Update_DelegatesToRepo(t *testing.T) {
	repo := &fakeProductRepo{
		updateHeaderResult: domain.Product{ID: uuid.New(), Title: "Updated"},
	}
	uc := NewProductUpdateUseCase(repo)

	id := uuid.New()
	title := "Updated"
	got, err := uc.Update(context.Background(), id, domain.UpdateProductInput{Title: &title})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.Title != "Updated" {
		t.Errorf("expected title Updated, got %q", got.Title)
	}
	if repo.updateHeaderID != id {
		t.Errorf("expected id %s, got %s", id, repo.updateHeaderID)
	}
	if repo.updateHeaderInput.Title == nil || *repo.updateHeaderInput.Title != "Updated" {
		t.Errorf("expected title Updated to be passed through, got %v", repo.updateHeaderInput.Title)
	}
}

func TestUpdateProductUseCase_Update_PropagatesError(t *testing.T) {
	repo := &fakeProductRepo{updateHeaderErr: domain.ErrProductNotFound}
	uc := NewProductUpdateUseCase(repo)

	_, err := uc.Update(context.Background(), uuid.New(), domain.UpdateProductInput{})
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestUpdateProductUseCase_Archive(t *testing.T) {
	id := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{Product: domain.Product{ID: id, Status: domain.ProductStatusDraft}},
	}
	uc := NewProductUpdateUseCase(repo)

	if err := uc.Archive(context.Background(), id); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.updateStatusID != id {
		t.Errorf("expected id %s, got %s", id, repo.updateStatusID)
	}
	if repo.updateStatusStatus != domain.ProductStatusArchived {
		t.Errorf("expected archived status, got %q", repo.updateStatusStatus)
	}
}

func TestUpdateProductUseCase_Archive_AlreadyArchived(t *testing.T) {
	id := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{Product: domain.Product{ID: id, Status: domain.ProductStatusArchived}},
	}
	uc := NewProductUpdateUseCase(repo)

	err := uc.Archive(context.Background(), id)
	if err != domain.ErrProductInvalidStatusTransition {
		t.Fatalf("expected ErrProductInvalidStatusTransition, got %v", err)
	}
}

func TestUpdateProductUseCase_Restore(t *testing.T) {
	id := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{Product: domain.Product{ID: id, Status: domain.ProductStatusArchived}},
	}
	uc := NewProductUpdateUseCase(repo)

	if err := uc.Restore(context.Background(), id); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.updateStatusStatus != domain.ProductStatusActive {
		t.Errorf("expected active status, got %q", repo.updateStatusStatus)
	}
}

func TestUpdateProductUseCase_Restore_NotArchived(t *testing.T) {
	id := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{Product: domain.Product{ID: id, Status: domain.ProductStatusActive}},
	}
	uc := NewProductUpdateUseCase(repo)

	err := uc.Restore(context.Background(), id)
	if err != domain.ErrProductInvalidStatusTransition {
		t.Fatalf("expected ErrProductInvalidStatusTransition, got %v", err)
	}
}

func TestDeleteProductUseCase_Purge(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductDeleteUseCase(repo)

	id := uuid.New()
	if err := uc.Purge(context.Background(), id); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.deleteID != id {
		t.Errorf("expected id %s, got %s", id, repo.deleteID)
	}
}

func TestOptionUseCase_Create_GeneratesIDsAndPosition(t *testing.T) {
	productID := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{
			Product: domain.Product{
				ID: productID,
				Options: []domain.ProductOption{
					{ID: uuid.New(), Position: 0},
					{ID: uuid.New(), Position: 1},
				},
			},
		},
	}
	uc := NewOptionUseCase(repo, repo)

	created, err := uc.Create(context.Background(), productID, domain.CreateOptionInput{
		Name:   "Color",
		Values: []string{"Red", "Blue"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("expected generated option ID")
	}
	if created.ProductID != productID {
		t.Errorf("expected product ID %s, got %s", productID, created.ProductID)
	}
	if created.Position != 2 {
		t.Errorf("expected next position 2, got %d", created.Position)
	}
	if len(created.Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(created.Values))
	}
	for i, v := range created.Values {
		if v.ID == uuid.Nil {
			t.Errorf("value %d has nil ID", i)
		}
		if v.OptionID != created.ID {
			t.Errorf("value %d OptionID %s does not match option ID %s", i, v.OptionID, created.ID)
		}
		if v.Position != i {
			t.Errorf("value %d position %d, expected %d", i, v.Position, i)
		}
	}
	if repo.createOption.ID != created.ID {
		t.Error("repo did not receive the created option")
	}
}

func TestOptionUseCase_Create_ProductNotFound(t *testing.T) {
	repo := &fakeProductRepo{findByIDErr: domain.ErrProductNotFound}
	uc := NewOptionUseCase(repo, repo)

	_, err := uc.Create(context.Background(), uuid.New(), domain.CreateOptionInput{Name: "Color", Values: []string{"Red"}})
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestOptionUseCase_Rename(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewOptionUseCase(repo, repo)

	productID := uuid.New()
	optionID := uuid.New()
	got, err := uc.Rename(context.Background(), productID, optionID, domain.UpdateOptionInput{Name: "Size"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.Name != "Size" {
		t.Errorf("expected name Size, got %q", got.Name)
	}
	if repo.renameOptionProductID != productID {
		t.Errorf("expected product id %s, got %s", productID, repo.renameOptionProductID)
	}
	if repo.renameOptionID != optionID {
		t.Errorf("expected option id %s, got %s", optionID, repo.renameOptionID)
	}
	if repo.renameOptionName != "Size" {
		t.Errorf("expected name Size, got %q", repo.renameOptionName)
	}
}

func TestOptionUseCase_Delete(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewOptionUseCase(repo, repo)

	productID := uuid.New()
	optionID := uuid.New()
	if err := uc.Delete(context.Background(), productID, optionID); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.deleteOptionProductID != productID || repo.deleteOptionID != optionID {
		t.Errorf("expected ids to be passed through, got %s/%s", repo.deleteOptionProductID, repo.deleteOptionID)
	}
}

func TestOptionUseCase_Reorder(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewOptionUseCase(repo, repo)

	productID := uuid.New()
	positions := []domain.PositionUpdate{{ID: uuid.New(), Position: 0}, {ID: uuid.New(), Position: 1}}
	if err := uc.Reorder(context.Background(), productID, domain.ReorderOptionsInput{Positions: positions}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.reorderProductID != productID {
		t.Errorf("expected product id %s, got %s", productID, repo.reorderProductID)
	}
	if len(repo.reorderPositions) != 2 {
		t.Errorf("expected 2 positions, got %d", len(repo.reorderPositions))
	}
}

func TestOptionUseCase_AddValue_GeneratesID(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewOptionUseCase(repo, repo)

	optionID := uuid.New()
	created, err := uc.AddValue(context.Background(), uuid.New(), optionID, domain.CreateOptionValueInput{Value: "Red", Position: 3})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("expected generated value ID")
	}
	if repo.createOptionValue.OptionID != optionID {
		t.Errorf("expected option id %s, got %s", optionID, repo.createOptionValue.OptionID)
	}
	if repo.createOptionValue.Value != "Red" || repo.createOptionValue.Position != 3 {
		t.Errorf("unexpected value passed to repo: %+v", repo.createOptionValue)
	}
}

func TestOptionUseCase_UpdateValue(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewOptionUseCase(repo, repo)

	valueID := uuid.New()
	value := "Crimson"
	position := 5
	got, err := uc.UpdateValue(context.Background(), uuid.New(), uuid.New(), valueID, domain.UpdateOptionValueInput{
		Value:    &value,
		Position: &position,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.ID != valueID {
		t.Errorf("expected value id %s, got %s", valueID, got.ID)
	}
	if repo.updateOptionValueID != valueID {
		t.Errorf("expected value id %s, got %s", valueID, repo.updateOptionValueID)
	}
	if repo.updateOptionValueValue == nil || *repo.updateOptionValueValue != "Crimson" {
		t.Errorf("expected value Crimson passed through")
	}
	if repo.updateOptionValuePosition == nil || *repo.updateOptionValuePosition != 5 {
		t.Errorf("expected position 5 passed through")
	}
}

func TestOptionUseCase_DeleteValue(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewOptionUseCase(repo, repo)

	valueID := uuid.New()
	if err := uc.DeleteValue(context.Background(), uuid.New(), uuid.New(), valueID); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.deleteOptionValueID != valueID {
		t.Errorf("expected value id %s, got %s", valueID, repo.deleteOptionValueID)
	}
}
