package product

import (
	"context"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type optionUc struct {
	productRepo domain.ProductRepository
}

func NewOptionUseCase(productRepo domain.ProductRepository) domain.OptionUseCase {
	return &optionUc{
		productRepo: productRepo,
	}
}

func (uc *optionUc) Create(ctx context.Context, productID uuid.UUID, input domain.CreateOptionInput) (domain.ProductOption, error) {

	// Resolve the product to confirm it exists and compute the next option
	// position from the number of already-existing options.
	product, err := uc.productRepo.FindByID(ctx, productID)
	if err != nil {
		logger.L(ctx).Error("create option failed", zap.Error(err), zap.String("product_id", productID.String()))
		return domain.ProductOption{}, err
	}

	optionID := uuid.Must(uuid.NewV7())

	option := domain.ProductOption{
		ID:        optionID,
		ProductID: productID,
		Name:      input.Name,
		Position:  len(product.Options),
		Values:    make([]domain.ProductOptionValue, 0, len(input.Values)),
	}

	for i, v := range input.Values {
		option.Values = append(option.Values, domain.ProductOptionValue{
			ID:       uuid.Must(uuid.NewV7()),
			OptionID: optionID,
			Value:    v,
			Position: i,
		})
	}

	created, err := uc.productRepo.CreateOption(ctx, option)
	if err != nil {
		logger.L(ctx).Error("create option failed", zap.Error(err), zap.String("product_id", productID.String()), zap.String("option_id", optionID.String()))
		return domain.ProductOption{}, err
	}

	logger.L(ctx).Info("[INFO] Success create option", zap.String("option_id", created.ID.String()))

	return created, nil
}

func (uc *optionUc) Rename(ctx context.Context, productID, optionID uuid.UUID, input domain.UpdateOptionInput) (domain.ProductOption, error) {

	renamed, err := uc.productRepo.RenameOption(ctx, productID, optionID, input.Name)
	if err != nil {
		logger.L(ctx).Error("rename option failed", zap.Error(err), zap.String("product_id", productID.String()), zap.String("option_id", optionID.String()))
		return domain.ProductOption{}, err
	}

	logger.L(ctx).Info("[INFO] Success rename option", zap.String("option_id", renamed.ID.String()))

	return renamed, nil
}

func (uc *optionUc) Delete(ctx context.Context, productID, optionID uuid.UUID) error {

	err := uc.productRepo.DeleteOption(ctx, productID, optionID)
	if err != nil {
		logger.L(ctx).Error("delete option failed", zap.Error(err), zap.String("product_id", productID.String()), zap.String("option_id", optionID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success delete option", zap.String("option_id", optionID.String()))

	return nil
}

func (uc *optionUc) Reorder(ctx context.Context, productID uuid.UUID, input domain.ReorderOptionsInput) error {

	err := uc.productRepo.ReorderOptions(ctx, productID, input.Positions)
	if err != nil {
		logger.L(ctx).Error("reorder options failed", zap.Error(err), zap.String("product_id", productID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success reorder options", zap.String("product_id", productID.String()))

	return nil
}

func (uc *optionUc) AddValue(ctx context.Context, productID, optionID uuid.UUID, input domain.CreateOptionValueInput) (domain.ProductOptionValue, error) {

	valueID := uuid.Must(uuid.NewV7())

	value := domain.ProductOptionValue{
		ID:       valueID,
		OptionID: optionID,
		Value:    input.Value,
		Position: input.Position,
	}

	created, err := uc.productRepo.CreateOptionValue(ctx, value)
	if err != nil {
		logger.L(ctx).Error("add option value failed", zap.Error(err), zap.String("product_id", productID.String()), zap.String("option_id", optionID.String()))
		return domain.ProductOptionValue{}, err
	}

	logger.L(ctx).Info("[INFO] Success add option value", zap.String("value_id", created.ID.String()))

	return created, nil
}

func (uc *optionUc) UpdateValue(ctx context.Context, productID, optionID, valueID uuid.UUID, input domain.UpdateOptionValueInput) (domain.ProductOptionValue, error) {

	updated, err := uc.productRepo.UpdateOptionValue(ctx, valueID, input.Value, input.Position)
	if err != nil {
		logger.L(ctx).Error("update option value failed", zap.Error(err), zap.String("product_id", productID.String()), zap.String("option_id", optionID.String()), zap.String("value_id", valueID.String()))
		return domain.ProductOptionValue{}, err
	}

	logger.L(ctx).Info("[INFO] Success update option value", zap.String("value_id", updated.ID.String()))

	return updated, nil
}

func (uc *optionUc) DeleteValue(ctx context.Context, productID, optionID, valueID uuid.UUID) error {

	err := uc.productRepo.DeleteOptionValue(ctx, valueID)
	if err != nil {
		logger.L(ctx).Error("delete option value failed", zap.Error(err), zap.String("product_id", productID.String()), zap.String("option_id", optionID.String()), zap.String("value_id", valueID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success delete option value", zap.String("value_id", valueID.String()))

	return nil
}
