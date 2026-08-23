package product

import (
	"context"
	"math"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type queryProductUc struct {
	productRepo domain.ProductRepository
}

func NewProductQueryUseCase(productRepo domain.ProductRepository) domain.QueryProductUseCase {
	return &queryProductUc{
		productRepo: productRepo,
	}
}

func (uc *queryProductUc) FindAll(ctx context.Context, params domain.ListProductParams) (domain.PaginatedProducts, error) {

	// Apply defaults if zero-valued.
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PerPage < 1 {
		params.PerPage = 10
	}
	if params.SortBy == "" {
		params.SortBy = "created_at"
	}
	if params.SortDir == "" {
		params.SortDir = "desc"
	}

	if params.Status != "" && !isValidProductStatus(domain.ProductStatus(params.Status)) {
		return domain.PaginatedProducts{}, domain.ErrProductInvalidStatus
	}

	data, total, err := uc.productRepo.FindAll(ctx, params)
	if err != nil {
		logger.L(ctx).Error("find all products failed", zap.Error(err))
		return domain.PaginatedProducts{}, err
	}

	totalPages := int(math.Ceil(float64(total) / float64(params.PerPage)))

	result := domain.PaginatedProducts{
		Data:       data,
		Total:      total,
		Page:       params.Page,
		PerPage:    params.PerPage,
		TotalPages: totalPages,
	}

	logger.L(ctx).Info("[INFO] Success get all products")

	return result, nil
}

func (uc *queryProductUc) GetByID(ctx context.Context, id uuid.UUID) (domain.ProductDetail, error) {

	data, err := uc.productRepo.FindByID(ctx, id)
	if err != nil {
		if err != domain.ErrProductNotFound {
			logger.L(ctx).Error("find product by id failed", zap.Error(err), zap.String("id", id.String()))
		}
		return domain.ProductDetail{}, err
	}

	logger.L(ctx).Info("[INFO] Success get product by id", zap.String("id", id.String()))

	return data, nil
}

func (uc *queryProductUc) GetByHandle(ctx context.Context, handle string) (domain.ProductDetail, error) {

	data, err := uc.productRepo.FindByHandle(ctx, handle)
	if err != nil {
		if err != domain.ErrProductNotFound {
			logger.L(ctx).Error("find product by handle failed", zap.Error(err), zap.String("handle", handle))
		}
		return domain.ProductDetail{}, err
	}

	logger.L(ctx).Info("[INFO] Success get product by handle", zap.String("handle", handle))

	return data, nil
}
