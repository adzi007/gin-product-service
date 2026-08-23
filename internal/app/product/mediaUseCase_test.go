package product

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestMediaUseCase_Create_GeneratesIDsAndPositions(t *testing.T) {
	productID := uuid.New()
	repo := &fakeProductRepo{
		findByIDData: domain.ProductDetail{
			Product: domain.Product{
				ID:    productID,
				Media: []domain.ProductMedia{{ID: uuid.New(), Position: 0}},
			},
		},
	}
	uc := NewMediaUseCase(repo, repo, repo)

	created, err := uc.Create(context.Background(), productID, domain.BulkCreateMediaInput{
		Media: []domain.CreateMediaInput{
			{Type: "image", URL: "https://cdn.example.com/a.jpg"},
			{Type: "video", URL: "https://cdn.example.com/b.mp4"},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 media, got %d", len(created))
	}
	for i, m := range created {
		if m.ID == uuid.Nil {
			t.Errorf("media %d has nil ID", i)
		}
		if m.ProductID != productID {
			t.Errorf("media %d product id %s, expected %s", i, m.ProductID, productID)
		}
		if m.Position != 1+i {
			t.Errorf("media %d position %d, expected %d", i, m.Position, 1+i)
		}
	}
	if len(repo.createProductMedia) != 2 {
		t.Errorf("repo did not receive 2 media, got %d", len(repo.createProductMedia))
	}
}

func TestMediaUseCase_Create_ProductNotFound(t *testing.T) {
	repo := &fakeProductRepo{findByIDErr: domain.ErrProductNotFound}
	uc := NewMediaUseCase(repo, repo, repo)

	_, err := uc.Create(context.Background(), uuid.New(), domain.BulkCreateMediaInput{
		Media: []domain.CreateMediaInput{{Type: "image", URL: "https://cdn.example.com/a.jpg"}},
	})
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestMediaUseCase_Update_Success(t *testing.T) {
	productID := uuid.New()
	mediaID := uuid.New()
	repo := &fakeProductRepo{
		findMediaByIDData: domain.ProductMedia{ID: mediaID, ProductID: productID},
	}
	uc := NewMediaUseCase(repo, repo, repo)

	alt := "front view"
	got, err := uc.Update(context.Background(), productID, mediaID, domain.UpdateMediaInput{AltText: &alt})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.ID != mediaID {
		t.Errorf("expected media id %s, got %s", mediaID, got.ID)
	}
	if repo.updateProductMediaID != mediaID {
		t.Errorf("expected update for media id %s, got %s", mediaID, repo.updateProductMediaID)
	}
	if repo.updateProductMediaAltText == nil || *repo.updateProductMediaAltText != alt {
		t.Errorf("expected alt text %q passed through, got %v", alt, repo.updateProductMediaAltText)
	}
}

func TestMediaUseCase_Update_OwnershipMismatch(t *testing.T) {
	productID := uuid.New()
	mediaID := uuid.New()
	otherProduct := uuid.New()
	repo := &fakeProductRepo{
		findMediaByIDData: domain.ProductMedia{ID: mediaID, ProductID: otherProduct},
	}
	uc := NewMediaUseCase(repo, repo, repo)

	_, err := uc.Update(context.Background(), productID, mediaID, domain.UpdateMediaInput{})
	if err != domain.ErrMediaNotFound {
		t.Fatalf("expected ErrMediaNotFound, got %v", err)
	}
}

func TestMediaUseCase_Delete_Success(t *testing.T) {
	productID := uuid.New()
	mediaID := uuid.New()
	repo := &fakeProductRepo{
		findMediaByIDData: domain.ProductMedia{ID: mediaID, ProductID: productID},
	}
	uc := NewMediaUseCase(repo, repo, repo)

	if err := uc.Delete(context.Background(), productID, mediaID); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.deleteProductMediaID != mediaID {
		t.Errorf("expected delete for media id %s, got %s", mediaID, repo.deleteProductMediaID)
	}
}

func TestMediaUseCase_AttachToVariant_Success(t *testing.T) {
	variantID := uuid.New()
	mediaID := uuid.New()
	productID := uuid.New()
	repo := &fakeProductRepo{
		findVariantByIDData: domain.Variant{ID: variantID, ProductID: productID},
		findMediaByIDData:   domain.ProductMedia{ID: mediaID, ProductID: productID},
	}
	uc := NewMediaUseCase(repo, repo, repo)

	link, err := uc.AttachToVariant(context.Background(), variantID, domain.AttachVariantMediaInput{MediaID: mediaID})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if link.VariantID != variantID || link.MediaID != mediaID {
		t.Errorf("unexpected link: %+v", link)
	}
	if repo.attachVariantMediaVariantID != variantID || repo.attachVariantMediaMediaID != mediaID {
		t.Errorf("repo did not receive expected ids")
	}
}

func TestMediaUseCase_AttachToVariant_OwnershipMismatch(t *testing.T) {
	variantID := uuid.New()
	mediaID := uuid.New()
	productID := uuid.New()
	otherProduct := uuid.New()
	repo := &fakeProductRepo{
		findVariantByIDData: domain.Variant{ID: variantID, ProductID: productID},
		findMediaByIDData:   domain.ProductMedia{ID: mediaID, ProductID: otherProduct},
	}
	uc := NewMediaUseCase(repo, repo, repo)

	_, err := uc.AttachToVariant(context.Background(), variantID, domain.AttachVariantMediaInput{MediaID: mediaID})
	if err != domain.ErrMediaNotFound {
		t.Fatalf("expected ErrMediaNotFound, got %v", err)
	}
}

func TestMediaUseCase_AttachToVariant_VariantNotFound(t *testing.T) {
	repo := &fakeProductRepo{findVariantByIDErr: domain.ErrVariantNotFound}
	uc := NewMediaUseCase(repo, repo, repo)

	_, err := uc.AttachToVariant(context.Background(), uuid.New(), domain.AttachVariantMediaInput{MediaID: uuid.New()})
	if err != domain.ErrVariantNotFound {
		t.Fatalf("expected ErrVariantNotFound, got %v", err)
	}
}

func TestMediaUseCase_DetachFromVariant(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewMediaUseCase(repo, repo, repo)

	variantID := uuid.New()
	mediaID := uuid.New()
	if err := uc.DetachFromVariant(context.Background(), variantID, mediaID); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.detachVariantMediaVariantID != variantID || repo.detachVariantMediaMediaID != mediaID {
		t.Errorf("repo did not receive expected ids")
	}
}

func TestMediaUseCase_ReorderVariantMedia(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewMediaUseCase(repo, repo, repo)

	variantID := uuid.New()
	positions := []domain.PositionUpdate{{ID: uuid.New(), Position: 0}, {ID: uuid.New(), Position: 1}}
	if err := uc.ReorderVariantMedia(context.Background(), variantID, positions); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.reorderVariantMediaVariantID != variantID {
		t.Errorf("expected variant id %s, got %s", variantID, repo.reorderVariantMediaVariantID)
	}
	if len(repo.reorderVariantMediaPositions) != 2 {
		t.Errorf("expected 2 positions, got %d", len(repo.reorderVariantMediaPositions))
	}
}
