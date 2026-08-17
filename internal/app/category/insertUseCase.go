package category

import (
	"context"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"strings"

	"go.uber.org/zap"
)

type insertCategoryUc struct {
	categoryRepo domain.CategoryRepository
}

func NewCategoryInsertUseCase(categoryRepo domain.CategoryRepository) domain.InsertCategoryUseCase {
	return &insertCategoryUc{
		categoryRepo: categoryRepo,
	}
}

func (uc *insertCategoryUc) Create(ctx context.Context, input domain.CreateCategoryInput) (domain.Category, error) {

	// Guard against empty name even though Gin binding also enforces it.
	if strings.TrimSpace(input.Name) == "" {
		return domain.Category{}, domain.ErrCategoryInvalidInput
	}

	category := domain.Category{
		Name:        input.Name,
		Thumbnail:   input.Thumbnail,
		Description: input.Description,
	}

	created, err := uc.categoryRepo.Create(ctx, category)
	if err != nil {
		logger.L(ctx).Error("create category failed", zap.Error(err))
		return domain.Category{}, err
	}

	logger.L(ctx).Info("[INFO] Success create category", zap.Int("id", created.ID))

	return created, nil
}
