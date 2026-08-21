package product

import (
	"context"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type deleteProductUc struct {
	productRepo domain.ProductRepository
}

func NewProductDeleteUseCase(productRepo domain.ProductRepository) domain.DeleteProductUseCase {
	return &deleteProductUc{
		productRepo: productRepo,
	}
}

func (uc *deleteProductUc) Purge(ctx context.Context, id uuid.UUID) error {

	err := uc.productRepo.Delete(ctx, id)
	if err != nil {
		logger.L(ctx).Error("purge product failed", zap.Error(err), zap.String("id", id.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success purge product", zap.String("id", id.String()))

	return nil
}
