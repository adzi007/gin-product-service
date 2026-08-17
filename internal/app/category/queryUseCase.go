package category

import (
	"context"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"math"

	"go.uber.org/zap"
)

type queryCategoryUc struct {
	categoryRepo domain.CategoryRepository
}

func NewCategoryQueryUseCase(categoryRepo domain.CategoryRepository) domain.QueryCategoryUseCase {
	return &queryCategoryUc{
		categoryRepo: categoryRepo,
	}
}

func (uc *queryCategoryUc) FindAll(ctx context.Context, params domain.ListCategoryParams) (domain.PaginatedCategories, error) {

	// Apply defaults if zero-valued.
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PerPage < 1 {
		params.PerPage = 10
	}
	if params.SortBy == "" {
		params.SortBy = "name"
	}
	if params.SortDir == "" {
		params.SortDir = "asc"
	}
	data, total, err := uc.categoryRepo.FindAll(ctx, params)
	if err != nil {
		logger.L(ctx).Error("find all categories failed", zap.Error(err))
		return domain.PaginatedCategories{}, err
	}

	totalPages := int(math.Ceil(float64(total) / float64(params.PerPage)))

	result := domain.PaginatedCategories{
		Data:       data,
		Total:      total,
		Page:       params.Page,
		PerPage:    params.PerPage,
		TotalPages: totalPages,
	}

	logger.L(ctx).Info("[INFO] Success get all categories")

	return result, nil
}

func (uc *queryCategoryUc) FindAllForDropdown(ctx context.Context, name string) ([]domain.CategoryOption, error) {

	data, err := uc.categoryRepo.FindAllForDropdown(ctx, name)
	if err != nil {
		logger.L(ctx).Error("find all categories for dropdown failed", zap.Error(err))
		return nil, err
	}

	logger.L(ctx).Info("[INFO] Success get all categories for dropdown")

	return data, nil
}

func (uc *queryCategoryUc) GetByID(ctx context.Context, id int) (domain.Category, error) {

	data, err := uc.categoryRepo.FindByID(ctx, id)
	if err != nil {
		if err != domain.ErrCategoryNotFound {
			logger.L(ctx).Error("find category by id failed", zap.Error(err), zap.Int("id", id))
		}
		return domain.Category{}, err
	}

	logger.L(ctx).Info("[INFO] Success get category by id", zap.Int("id", id))

	return data, nil
}
