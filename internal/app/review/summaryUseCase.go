package review

import (
	"context"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type summaryReviewUc struct {
	reviewRepo domain.ReviewRepository
}

func NewReviewSummaryUseCase(reviewRepo domain.ReviewRepository) domain.SummaryReviewUseCase {
	return &summaryReviewUc{reviewRepo: reviewRepo}
}

func (uc *summaryReviewUc) GetSummary(ctx context.Context, productID uuid.UUID) (domain.RatingSummary, error) {

	summary, err := uc.reviewRepo.GetSummary(ctx, productID)
	if err != nil {
		return domain.RatingSummary{}, err
	}

	return *summary, nil
}
