package review

import (
	"context"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type deleteReviewUc struct {
	reviewRepo domain.ReviewRepository
}

func NewReviewDeleteUseCase(reviewRepo domain.ReviewRepository) domain.DeleteReviewUseCase {
	return &deleteReviewUc{reviewRepo: reviewRepo}
}

func (uc *deleteReviewUc) Delete(ctx context.Context, id, userID uuid.UUID) error {

	existing, err := uc.reviewRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if existing.UserID != userID {
		return domain.ErrReviewForbidden
	}

	return uc.reviewRepo.Delete(ctx, id)
}
