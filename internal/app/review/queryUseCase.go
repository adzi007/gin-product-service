package review

import (
	"context"
	"math"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type queryReviewUc struct {
	reviewRepo domain.ReviewRepository
}

func NewReviewQueryUseCase(reviewRepo domain.ReviewRepository) domain.QueryReviewUseCase {
	return &queryReviewUc{reviewRepo: reviewRepo}
}

func (uc *queryReviewUc) ListByProduct(ctx context.Context, productID uuid.UUID, filter domain.ReviewFilter) (domain.PaginatedReviews, error) {

	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	if filter.Sort == "" {
		filter.Sort = "newest"
	}

	data, total, err := uc.reviewRepo.FindByProduct(ctx, productID, filter)
	if err != nil {
		return domain.PaginatedReviews{}, err
	}

	totalPages := int(math.Ceil(float64(total) / float64(filter.Limit)))

	return domain.PaginatedReviews{
		Data:       data,
		Page:       filter.Page,
		Limit:      filter.Limit,
		TotalItems: total,
		TotalPages: totalPages,
	}, nil
}

func (uc *queryReviewUc) GetByID(ctx context.Context, id uuid.UUID) (domain.Review, error) {

	review, err := uc.reviewRepo.FindByID(ctx, id)
	if err != nil {
		return domain.Review{}, err
	}

	return *review, nil
}

func (uc *queryReviewUc) ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) (domain.PaginatedUserReviews, error) {

	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	data, total, err := uc.reviewRepo.FindByUser(ctx, userID, page, limit)
	if err != nil {
		return domain.PaginatedUserReviews{}, err
	}

	totalPages := int(math.Ceil(float64(total) / float64(limit)))

	return domain.PaginatedUserReviews{
		Data:       data,
		Page:       page,
		Limit:      limit,
		TotalItems: total,
		TotalPages: totalPages,
	}, nil
}
