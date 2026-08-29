package review

import (
	"context"
	"errors"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestUpdateReviewUseCase_NonOwnerForbidden(t *testing.T) {
	owner := uuid.New()
	repo := &fakeReviewRepo{
		findByIDData: &domain.Review{ID: uuid.New(), UserID: owner, Rating: 5},
	}
	uc := NewReviewUpdateUseCase(repo)

	_, err := uc.Update(context.Background(), repo.findByIDData.ID, uuid.New(), "Intruder", domain.UpdateReviewInput{Rating: intPtr(4)})
	if !errors.Is(err, domain.ErrReviewForbidden) {
		t.Fatalf("expected ErrReviewForbidden, got %v", err)
	}
}

func TestUpdateReviewUseCase_Success(t *testing.T) {
	owner := uuid.New()
	id := uuid.New()
	repo := &fakeReviewRepo{
		findByIDData: &domain.Review{ID: id, UserID: owner, Rating: 5, DisplayName: "Old Name"},
	}
	uc := NewReviewUpdateUseCase(repo)

	updated, err := uc.Update(context.Background(), id, owner, "New Name", domain.UpdateReviewInput{Rating: intPtr(4)})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if updated.Rating != 4 {
		t.Fatalf("expected rating 4, got %d", updated.Rating)
	}
	if updated.DisplayName != "New Name" {
		t.Fatalf("expected display name New Name, got %s", updated.DisplayName)
	}
	if repo.updated == nil || repo.updated.ID != id {
		t.Fatal("expected repository Update to be called with the loaded review")
	}
}

func TestDeleteReviewUseCase_NonOwnerForbidden(t *testing.T) {
	owner := uuid.New()
	id := uuid.New()
	repo := &fakeReviewRepo{
		findByIDData: &domain.Review{ID: id, UserID: owner},
	}
	uc := NewReviewDeleteUseCase(repo)

	err := uc.Delete(context.Background(), id, uuid.New())
	if !errors.Is(err, domain.ErrReviewForbidden) {
		t.Fatalf("expected ErrReviewForbidden, got %v", err)
	}
}

func intPtr(v int) *int { return &v }
