package product

import (
	"context"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type updateProductUc struct {
	productRepo domain.ProductRepository
}

func NewProductUpdateUseCase(productRepo domain.ProductRepository) domain.UpdateProductUseCase {
	return &updateProductUc{
		productRepo: productRepo,
	}
}

func (uc *updateProductUc) Update(ctx context.Context, id uuid.UUID, input domain.UpdateProductInput) (domain.Product, error) {

	updated, err := uc.productRepo.UpdateHeader(ctx, id, input)
	if err != nil {
		logger.L(ctx).Error("update product failed", zap.Error(err), zap.String("id", id.String()))
		return domain.Product{}, err
	}

	logger.L(ctx).Info("[INFO] Success update product", zap.String("id", updated.ID.String()))

	return updated, nil
}

func (uc *updateProductUc) Archive(ctx context.Context, id uuid.UUID) error {

	err := uc.productRepo.UpdateStatus(ctx, id, domain.ProductStatusArchived)
	if err != nil {
		logger.L(ctx).Error("archive product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success archive product", zap.String("id", id.String()))

	return nil
}

func (uc *updateProductUc) Restore(ctx context.Context, id uuid.UUID) error {

	err := uc.productRepo.UpdateStatus(ctx, id, domain.ProductStatusActive)
	if err != nil {
		logger.L(ctx).Error("restore product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success restore product", zap.String("id", id.String()))

	return nil
}
