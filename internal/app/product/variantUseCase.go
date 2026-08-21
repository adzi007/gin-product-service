package product

import (
	"context"
	"encoding/json"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type variantUc struct {
	productRepo domain.ProductRepository
}

func NewVariantUseCase(productRepo domain.ProductRepository) domain.VariantUseCase {
	return &variantUc{
		productRepo: productRepo,
	}
}

func (uc *variantUc) Create(ctx context.Context, productID uuid.UUID, input domain.CreateVariantInput) (domain.Variant, error) {

	// Resolve the product to confirm it exists, validate the option selection
	// against it, and compute the next variant position.
	product, err := uc.productRepo.FindByID(ctx, productID)
	if err != nil {
		logger.L(ctx).Error("create variant failed", zap.Error(err), zap.String("product_id", productID.String()))
		return domain.Variant{}, err
	}

	optsJSON, err := buildVariantOptionsJSONFromOptions(product.Options, input.Options)
	if err != nil {
		logger.L(ctx).Error("create variant failed", zap.Error(err), zap.String("product_id", productID.String()))
		return domain.Variant{}, err
	}

	variant := domain.Variant{
		ID:        uuid.Must(uuid.NewV7()),
		ProductID: productID,
		SKU:       input.SKU,
		Barcode:   input.Barcode,
		Title:     input.Title,
		Price:     *input.Price,
		Weight:    *input.Weight,
		Options:   optsJSON,
		Position:  len(product.Variants),
		IsDeleted: false,
	}

	created, err := uc.productRepo.CreateVariant(ctx, variant)
	if err != nil {
		logger.L(ctx).Error("create variant failed", zap.Error(err), zap.String("product_id", productID.String()), zap.String("variant_id", variant.ID.String()))
		return domain.Variant{}, err
	}

	logger.L(ctx).Info("[INFO] Success create variant", zap.String("variant_id", created.ID.String()))

	return created, nil
}

func (uc *variantUc) BulkCreate(ctx context.Context, productID uuid.UUID, input domain.BulkCreateVariantsInput) ([]domain.Variant, error) {

	product, err := uc.productRepo.FindByID(ctx, productID)
	if err != nil {
		logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
		return nil, err
	}

	variants := make([]domain.Variant, 0, len(input.Variants))
	for i, v := range input.Variants {
		optsJSON, err := buildVariantOptionsJSONFromOptions(product.Options, v.Options)
		if err != nil {
			logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
			return nil, err
		}

		variants = append(variants, domain.Variant{
			ID:        uuid.Must(uuid.NewV7()),
			ProductID: productID,
			SKU:       v.SKU,
			Barcode:   v.Barcode,
			Title:     v.Title,
			Price:     *v.Price,
			Weight:    *v.Weight,
			Options:   optsJSON,
			Position:  len(product.Variants) + i,
			IsDeleted: false,
		})
	}

	created, err := uc.productRepo.CreateVariants(ctx, variants)
	if err != nil {
		logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
		return nil, err
	}

	logger.L(ctx).Info("[INFO] Success bulk create variants", zap.Int("count", len(created)))

	return created, nil
}

func (uc *variantUc) Update(ctx context.Context, variantID uuid.UUID, input domain.UpdateVariantInput) (domain.Variant, error) {

	updated, err := uc.productRepo.UpdateVariant(ctx, variantID, input)
	if err != nil {
		logger.L(ctx).Error("update variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
		return domain.Variant{}, err
	}

	logger.L(ctx).Info("[INFO] Success update variant", zap.String("variant_id", updated.ID.String()))

	return updated, nil
}

func (uc *variantUc) BulkUpdate(ctx context.Context, productID uuid.UUID, input domain.BulkUpdateVariantsInput) ([]domain.Variant, error) {

	updated := make([]domain.Variant, 0, len(input.Updates))
	for _, item := range input.Updates {
		v, err := uc.productRepo.UpdateVariant(ctx, item.ID, item.Fields)
		if err != nil {
			logger.L(ctx).Error("bulk update variants failed", zap.Error(err), zap.String("variant_id", item.ID.String()))
			return nil, err
		}
		updated = append(updated, v)
	}

	logger.L(ctx).Info("[INFO] Success bulk update variants", zap.Int("count", len(updated)))

	return updated, nil
}

func (uc *variantUc) Delete(ctx context.Context, variantID uuid.UUID) error {

	hasHistory, err := uc.productRepo.VariantHasHistory(ctx, variantID)
	if err != nil {
		logger.L(ctx).Error("delete variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
		return err
	}

	// Soft-delete when history exists, hard-delete otherwise.
	err = uc.productRepo.DeleteVariant(ctx, variantID, !hasHistory)
	if err != nil {
		logger.L(ctx).Error("delete variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success delete variant", zap.String("variant_id", variantID.String()))

	return nil
}

func (uc *variantUc) BulkDelete(ctx context.Context, productID uuid.UUID, input domain.BulkDeleteVariantsInput) error {

	var hardIDs, softIDs []uuid.UUID
	for _, id := range input.IDs {
		hasHistory, err := uc.productRepo.VariantHasHistory(ctx, id)
		if err != nil {
			logger.L(ctx).Error("bulk delete variants failed", zap.Error(err), zap.String("variant_id", id.String()))
			return err
		}
		if hasHistory {
			softIDs = append(softIDs, id)
		} else {
			hardIDs = append(hardIDs, id)
		}
	}

	if len(hardIDs) > 0 {
		if err := uc.productRepo.BulkDeleteVariants(ctx, hardIDs, true); err != nil {
			logger.L(ctx).Error("bulk delete variants failed", zap.Error(err), zap.String("product_id", productID.String()))
			return err
		}
	}
	if len(softIDs) > 0 {
		if err := uc.productRepo.BulkDeleteVariants(ctx, softIDs, false); err != nil {
			logger.L(ctx).Error("bulk delete variants failed", zap.Error(err), zap.String("product_id", productID.String()))
			return err
		}
	}

	logger.L(ctx).Info("[INFO] Success bulk delete variants", zap.Int("count", len(input.IDs)))

	return nil
}

func (uc *variantUc) Restore(ctx context.Context, variantID uuid.UUID) (domain.Variant, error) {

	restored, err := uc.productRepo.RestoreVariant(ctx, variantID)
	if err != nil {
		logger.L(ctx).Error("restore variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
		return domain.Variant{}, err
	}

	logger.L(ctx).Info("[INFO] Success restore variant", zap.String("variant_id", restored.ID.String()))

	return restored, nil
}

func (uc *variantUc) Reorder(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {

	err := uc.productRepo.ReorderVariants(ctx, productID, positions)
	if err != nil {
		logger.L(ctx).Error("reorder variants failed", zap.Error(err), zap.String("product_id", productID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success reorder variants", zap.String("product_id", productID.String()))

	return nil
}

// buildVariantOptionsJSONFromOptions validates and serializes the selected
// option map against the product's current options, producing the same JSONB
// shape ([{"option":"Color","value":"Black"}]) used at product creation.
func buildVariantOptionsJSONFromOptions(declared []domain.ProductOption, selected map[string]string) ([]byte, error) {

	valueSetByOption := make(map[string]map[string]struct{}, len(declared))
	orderedNames := make([]string, 0, len(declared))
	for _, opt := range declared {
		set := make(map[string]struct{}, len(opt.Values))
		for _, v := range opt.Values {
			set[v.Value] = struct{}{}
		}
		valueSetByOption[opt.Name] = set
		orderedNames = append(orderedNames, opt.Name)
	}

	// Every selected key must reference a declared option.
	for name := range selected {
		if _, ok := valueSetByOption[name]; !ok {
			return nil, domain.ErrInvalidOption
		}
	}

	opts := make([]domain.VariantOption, 0, len(selected))
	for _, name := range orderedNames {
		value, ok := selected[name]
		if !ok {
			continue
		}
		if _, exists := valueSetByOption[name][value]; !exists {
			return nil, domain.ErrInvalidOption
		}
		opts = append(opts, domain.VariantOption{Option: name, Value: value})
	}

	return json.Marshal(opts)
}
