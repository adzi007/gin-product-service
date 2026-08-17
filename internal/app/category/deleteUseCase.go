package category

import (
	"context"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"go.uber.org/zap"
)

type deleteCategoryUc struct {
	categoryRepo domain.CategoryRepository
}

func NewCategoryDeleteUseCase(categoryRepo domain.CategoryRepository) domain.DeleteCategoryUseCase {
	return &deleteCategoryUc{
		categoryRepo: categoryRepo,
	}
}

func (uc *deleteCategoryUc) Delete(ctx context.Context, id int) error {

	err := uc.categoryRepo.Delete(ctx, id)
	if err != nil {
		logger.L(ctx).Error("delete category failed", zap.Error(err), zap.Int("id", id))
		return err
	}

	logger.L(ctx).Info("[INFO] Success delete category", zap.Int("id", id))

	return nil
}
