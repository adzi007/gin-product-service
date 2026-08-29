package review

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestQueryReviewUseCase_ListByProductAppliesDefaults(t *testing.T) {
	repo := &fakeReviewRepo{
		findByProductData:  []domain.Review{{ID: uuid.New(), Rating: 5}},
		findByProductTotal: 1,
	}
	uc := NewReviewQueryUseCase(repo)

	result, err := uc.ListByProduct(context.Background(), uuid.New(), domain.ReviewFilter{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Page != 1 {
		t.Fatalf("expected page 1, got %d", result.Page)
	}
	if result.Limit != 20 {
		t.Fatalf("expected limit 20, got %d", result.Limit)
	}
	if result.TotalItems != 1 || result.TotalPages != 1 {
		t.Fatalf("unexpected pagination metadata: %+v", result)
	}
	if repo.findByProductFilter.Sort != "newest" {
		t.Fatalf("expected default sort newest, got %s", repo.findByProductFilter.Sort)
	}
}

func TestQueryReviewUseCase_ListByUserAppliesDefaults(t *testing.T) {
	repo := &fakeReviewRepo{
		findByUserData:  []domain.ReviewWithProduct{{ID: uuid.New(), Rating: 5}},
		findByUserTotal: 1,
	}
	uc := NewReviewQueryUseCase(repo)

	result, err := uc.ListByUser(context.Background(), uuid.New(), 0, 0)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Page != 1 || result.Limit != 20 {
		t.Fatalf("expected page 1 and limit 20, got %+v", result)
	}
}
