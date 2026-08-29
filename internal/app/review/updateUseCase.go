package review

import (
	"context"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type updateReviewUc struct {
	reviewRepo domain.ReviewRepository
}

func NewReviewUpdateUseCase(reviewRepo domain.ReviewRepository) domain.UpdateReviewUseCase {
	return &updateReviewUc{reviewRepo: reviewRepo}
}

func (uc *updateReviewUc) Update(ctx context.Context, id, userID uuid.UUID, displayName string, input domain.UpdateReviewInput) (domain.Review, error) {

	if input.Rating != nil && (*input.Rating < 1 || *input.Rating > 5) {
		return domain.Review{}, domain.ErrReviewInvalidInput
	}
	if input.Title != nil && len(*input.Title) > 150 {
		return domain.Review{}, domain.ErrReviewInvalidInput
	}
	if input.Comment != nil && len(*input.Comment) > 5000 {
		return domain.Review{}, domain.ErrReviewInvalidInput
	}

	existing, err := uc.reviewRepo.FindByID(ctx, id)
	if err != nil {
		return domain.Review{}, err
	}

	if existing.UserID != userID {
		return domain.Review{}, domain.ErrReviewForbidden
	}

	if input.Rating != nil {
		existing.Rating = *input.Rating
	}
	if input.Title != nil {
		existing.Title = normalizeOptionalText(input.Title)
	}
	if input.Comment != nil {
		existing.Comment = normalizeOptionalText(input.Comment)
	}
	// Re-save the display name in case the user's name changed since creation.
	existing.DisplayName = displayName

	if err := uc.reviewRepo.Update(ctx, existing); err != nil {
		return domain.Review{}, err
	}

	return *existing, nil
}
