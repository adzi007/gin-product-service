package category

import (
	"context"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"strings"

	"go.uber.org/zap"
)

type updateCategoryUc struct {
	categoryRepo domain.CategoryRepository
}

func NewCategoryUpdateUseCase(categoryRepo domain.CategoryRepository) domain.UpdateCategoryUseCase {
	return &updateCategoryUc{
		categoryRepo: categoryRepo,
	}
}

func (uc *updateCategoryUc) Update(ctx context.Context, id int, input domain.UpdateCategoryInput) (domain.Category, error) {

	if strings.TrimSpace(input.Name) == "" {
		return domain.Category{}, domain.ErrCategoryInvalidInput
	}

	category := domain.Category{
		Name:        input.Name,
		Thumbnail:   input.Thumbnail,
		Description: input.Description,
	}

	updated, err := uc.categoryRepo.Update(ctx, id, category)
	if err != nil {
		logger.L(ctx).Error("update category failed", zap.Error(err), zap.Int("id", id))
		return domain.Category{}, err
	}

	logger.L(ctx).Info("[INFO] Success update category", zap.Int("id", updated.ID))

	return updated, nil
}
