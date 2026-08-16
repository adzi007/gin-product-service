package category

import (
	"context"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"go.uber.org/zap"
)

type insertCategoryUc struct {
	categoryRepo domain.CategoryRepository
}

func NewCategoryQueryUseCase(categoryRepo domain.CategoryRepository) domain.QueryCategoryUseCase {
	return &insertCategoryUc{
		categoryRepo: categoryRepo,
	}
}

func (uc *insertCategoryUc) FindAll(ctx context.Context) ([]domain.Category, error) {

	data, err := uc.categoryRepo.FindAll(ctx)

	if err != nil {

		logger.L(ctx).Error("find all categories failed", zap.Error(err))
		return nil, err
	}

	logger.L(ctx).Info("[INFO] Success get all categories")

	return data, nil
}
