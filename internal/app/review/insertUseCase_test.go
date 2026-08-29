package review

import (
	"context"
	"errors"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestInsertReviewUseCase_CreateSuccess(t *testing.T) {
	repo := &fakeReviewRepo{productExists: true}
	uc := NewReviewInsertUseCase(repo)

	title := "Exactly what I needed"
	comment := "Great build quality, fast shipping."
	productID := uuid.New()
	userID := uuid.New()

	created, err := uc.Create(context.Background(), productID, userID, "Jordan K.", domain.CreateReviewInput{
		Rating:  5,
		Title:   &title,
		Comment: &comment,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("expected a generated review id")
	}
	if created.ProductID != productID {
		t.Fatalf("expected product id %s, got %s", productID, created.ProductID)
	}
	if created.UserID != userID {
		t.Fatalf("expected user id %s, got %s", userID, created.UserID)
	}
	if created.DisplayName != "Jordan K." {
		t.Fatalf("expected display name Jordan K., got %s", created.DisplayName)
	}
	if repo.inserted == nil || repo.inserted.ID != created.ID {
		t.Fatal("expected repository Insert to be called with the generated review")
	}
}

func TestInsertReviewUseCase_CreateDuplicate(t *testing.T) {
	repo := &fakeReviewRepo{productExists: true, insertErr: domain.ErrReviewAlreadyExists}
	uc := NewReviewInsertUseCase(repo)

	_, err := uc.Create(context.Background(), uuid.New(), uuid.New(), "Jordan K.", domain.CreateReviewInput{Rating: 5})
	if !errors.Is(err, domain.ErrReviewAlreadyExists) {
		t.Fatalf("expected ErrReviewAlreadyExists, got %v", err)
	}
}

func TestInsertReviewUseCase_VariantNotBelongingToProduct(t *testing.T) {
	repo := &fakeReviewRepo{productExists: true, variantBelongs: false}
	uc := NewReviewInsertUseCase(repo)

	productID := uuid.New()
	variantID := uuid.New()

	_, err := uc.Create(context.Background(), productID, uuid.New(), "Jordan K.", domain.CreateReviewInput{
		Rating:    5,
		VariantID: &variantID,
	})
	if !errors.Is(err, domain.ErrReviewVariantNotFound) {
		t.Fatalf("expected ErrReviewVariantNotFound, got %v", err)
	}
}

func TestInsertReviewUseCase_ProductNotFound(t *testing.T) {
	repo := &fakeReviewRepo{productExists: false}
	uc := NewReviewInsertUseCase(repo)

	_, err := uc.Create(context.Background(), uuid.New(), uuid.New(), "Jordan K.", domain.CreateReviewInput{Rating: 5})
	if !errors.Is(err, domain.ErrReviewProductNotFound) {
		t.Fatalf("expected ErrReviewProductNotFound, got %v", err)
	}
}

func TestInsertReviewUseCase_InvalidRating(t *testing.T) {
	repo := &fakeReviewRepo{productExists: true}
	uc := NewReviewInsertUseCase(repo)

	_, err := uc.Create(context.Background(), uuid.New(), uuid.New(), "Jordan K.", domain.CreateReviewInput{Rating: 6})
	if !errors.Is(err, domain.ErrReviewInvalidInput) {
		t.Fatalf("expected ErrReviewInvalidInput, got %v", err)
	}
}
