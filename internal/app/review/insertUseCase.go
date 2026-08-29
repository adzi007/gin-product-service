package review

import (
	"context"
	"strings"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type insertReviewUc struct {
	reviewRepo domain.ReviewRepository
}

func NewReviewInsertUseCase(reviewRepo domain.ReviewRepository) domain.InsertReviewUseCase {
	return &insertReviewUc{reviewRepo: reviewRepo}
}

// normalizeOptionalText trims optional free-text input. Empty-after-trim values
// are treated as "not provided" (nil) so the has_comment filter and the
// spec's "trimmed" wording behave consistently.
func normalizeOptionalText(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func (uc *insertReviewUc) Create(ctx context.Context, productID, userID uuid.UUID, displayName string, input domain.CreateReviewInput) (domain.Review, error) {

	if input.Rating < 1 || input.Rating > 5 {
		return domain.Review{}, domain.ErrReviewInvalidInput
	}

	title := normalizeOptionalText(input.Title)
	if title != nil && len(*title) > 150 {
		return domain.Review{}, domain.ErrReviewInvalidInput
	}
	comment := normalizeOptionalText(input.Comment)
	if comment != nil && len(*comment) > 5000 {
		return domain.Review{}, domain.ErrReviewInvalidInput
	}

	exists, err := uc.reviewRepo.ProductExists(ctx, productID)
	if err != nil {
		return domain.Review{}, err
	}
	if !exists {
		return domain.Review{}, domain.ErrReviewProductNotFound
	}

	if input.VariantID != nil {
		belongs, err := uc.reviewRepo.VariantBelongsToProduct(ctx, *input.VariantID, productID)
		if err != nil {
			return domain.Review{}, err
		}
		if !belongs {
			return domain.Review{}, domain.ErrReviewVariantNotFound
		}
	}

	review := domain.Review{
		ID:          uuid.Must(uuid.NewV7()),
		ProductID:   productID,
		VariantID:   input.VariantID,
		UserID:      userID,
		DisplayName: displayName,
		Rating:      input.Rating,
		Title:       title,
		Comment:     comment,
	}

	if err := uc.reviewRepo.Insert(ctx, &review); err != nil {
		return domain.Review{}, err
	}

	return review, nil
}
