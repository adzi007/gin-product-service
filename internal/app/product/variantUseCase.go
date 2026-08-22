package product

import (
	"context"
	"encoding/json"
	"strings"

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

	if err := validateCreateVariantInput(input); err != nil {
		return domain.Variant{}, err
	}

	// Resolve the product to confirm it exists, validate the option selection
	// against it, and compute the next variant position.
	product, err := uc.productRepo.FindByID(ctx, productID)
	if err != nil {
		logger.L(ctx).Error("create variant failed 1", zap.Error(err), zap.String("product_id", productID.String()))
		return domain.Variant{}, err
	}

	optsJSON, err := buildVariantOptionsJSONFromOptions(product.Options, input.Options)
	if err != nil {
		logger.L(ctx).Error("create variant failed 2", zap.Error(err), zap.String("product_id", productID.String()))
		return domain.Variant{}, err
	}

	variantID := uuid.Must(uuid.NewV7())
	variant := domain.Variant{
		ID:        variantID,
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

	inventoryItem, stockMove, inventoryLevel := buildVariantStockRows(variantID, input.TrackInventory, input.Stock)

	newMedia, variantMedia, err := uc.resolveVariantMedia(ctx, productID, variantID, input.Media)
	if err != nil {
		logger.L(ctx).Error("create variant failed 3", zap.Error(err), zap.String("product_id", productID.String()))
		return domain.Variant{}, err
	}

	createdVariants, err := uc.productRepo.CreateVariantsWithStock(ctx, domain.CreateVariantsParams{
		Variants:        []domain.Variant{variant},
		InventoryItems:  []domain.InventoryItem{inventoryItem},
		StockMoves:      []domain.StockMove{stockMove},
		InventoryLevels: []domain.InventoryLevel{inventoryLevel},
		NewMedia:        newMedia,
		VariantMedia:    variantMedia,
	})
	if err != nil {
		logger.L(ctx).Error("create variant failed 4", zap.Error(err), zap.String("product_id", productID.String()), zap.String("variant_id", variantID.String()))
		return domain.Variant{}, err
	}

	created := createdVariants[0]

	logger.L(ctx).Info("[INFO] Success create variant 5", zap.String("variant_id", created.ID.String()))

	return created, nil
}

func (uc *variantUc) BulkCreate(ctx context.Context, productID uuid.UUID, input domain.BulkCreateVariantsInput) ([]domain.Variant, error) {

	product, err := uc.productRepo.FindByID(ctx, productID)
	if err != nil {
		logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
		return nil, err
	}

	params := domain.CreateVariantsParams{
		Variants:        make([]domain.Variant, 0, len(input.Variants)),
		InventoryItems:  make([]domain.InventoryItem, 0, len(input.Variants)),
		StockMoves:      make([]domain.StockMove, 0, len(input.Variants)),
		InventoryLevels: make([]domain.InventoryLevel, 0, len(input.Variants)),
	}
	for i, v := range input.Variants {
		if err := validateCreateVariantInput(v); err != nil {
			return nil, err
		}

		optsJSON, err := buildVariantOptionsJSONFromOptions(product.Options, v.Options)
		if err != nil {
			logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
			return nil, err
		}

		variantID := uuid.Must(uuid.NewV7())
		variant := domain.Variant{
			ID:        variantID,
			ProductID: productID,
			SKU:       v.SKU,
			Barcode:   v.Barcode,
			Title:     v.Title,
			Price:     *v.Price,
			Weight:    *v.Weight,
			Options:   optsJSON,
			Position:  len(product.Variants) + i,
			IsDeleted: false,
		}
		params.Variants = append(params.Variants, variant)

		inventoryItem, stockMove, inventoryLevel := buildVariantStockRows(variantID, v.TrackInventory, v.Stock)
		params.InventoryItems = append(params.InventoryItems, inventoryItem)
		params.StockMoves = append(params.StockMoves, stockMove)
		params.InventoryLevels = append(params.InventoryLevels, inventoryLevel)

		newMedia, variantMedia, err := uc.resolveVariantMedia(ctx, productID, variantID, v.Media)
		if err != nil {
			logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
			return nil, err
		}
		params.NewMedia = append(params.NewMedia, newMedia...)
		params.VariantMedia = append(params.VariantMedia, variantMedia...)
	}

	created, err := uc.productRepo.CreateVariantsWithStock(ctx, params)
	if err != nil {
		logger.L(ctx).Error("bulk create variants failed", zap.Error(err), zap.String("product_id", productID.String()))
		return nil, err
	}

	logger.L(ctx).Info("[INFO] Success bulk create variants", zap.Int("count", len(created)))

	return created, nil
}

func (uc *variantUc) Update(ctx context.Context, variantID uuid.UUID, input domain.UpdateVariantInput) (domain.Variant, error) {

	if input.Stock != nil && *input.Stock < 0 {
		return domain.Variant{}, domain.ErrProductInvalidInput
	}
	if err := validateVariantMediaInput(input.Media); err != nil {
		return domain.Variant{}, err
	}

	updated, err := uc.productRepo.UpdateVariant(ctx, variantID, input)
	if err != nil {
		logger.L(ctx).Error("update variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
		return domain.Variant{}, err
	}

	if input.Stock != nil {
		if _, err := uc.productRepo.AdjustVariantStock(ctx, variantID, *input.Stock); err != nil {
			logger.L(ctx).Error("adjust variant stock failed", zap.Error(err), zap.String("variant_id", variantID.String()))
			return domain.Variant{}, err
		}
	}

	if len(input.Media) > 0 {
		variant, err := uc.productRepo.FindVariantByID(ctx, variantID)
		if err != nil {
			logger.L(ctx).Error("update variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
			return domain.Variant{}, err
		}
		newMedia, links, err := uc.resolveVariantMedia(ctx, variant.ProductID, variantID, input.Media)
		if err != nil {
			logger.L(ctx).Error("update variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
			return domain.Variant{}, err
		}
		if err := uc.productRepo.AttachNewOrExistingVariantMedia(ctx, newMedia, links); err != nil {
			logger.L(ctx).Error("update variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
			return domain.Variant{}, err
		}
	}

	logger.L(ctx).Info("[INFO] Success update variant", zap.String("variant_id", updated.ID.String()))

	return updated, nil
}

func (uc *variantUc) BulkUpdate(ctx context.Context, productID uuid.UUID, input domain.BulkUpdateVariantsInput) ([]domain.Variant, error) {

	updated := make([]domain.Variant, 0, len(input.Updates))
	for _, item := range input.Updates {
		if item.Fields.Stock != nil && *item.Fields.Stock < 0 {
			return nil, domain.ErrProductInvalidInput
		}
		if err := validateVariantMediaInput(item.Fields.Media); err != nil {
			return nil, err
		}

		v, err := uc.productRepo.UpdateVariant(ctx, item.ID, item.Fields)
		if err != nil {
			logger.L(ctx).Error("bulk update variants failed", zap.Error(err), zap.String("variant_id", item.ID.String()))
			return nil, err
		}
		updated = append(updated, v)

		if item.Fields.Stock != nil {
			if _, err := uc.productRepo.AdjustVariantStock(ctx, item.ID, *item.Fields.Stock); err != nil {
				logger.L(ctx).Error("bulk update variants failed", zap.Error(err), zap.String("variant_id", item.ID.String()))
				return nil, err
			}
		}

		if len(item.Fields.Media) > 0 {
			newMedia, links, err := uc.resolveVariantMedia(ctx, productID, item.ID, item.Fields.Media)
			if err != nil {
				logger.L(ctx).Error("bulk update variants failed", zap.Error(err), zap.String("variant_id", item.ID.String()))
				return nil, err
			}
			if err := uc.productRepo.AttachNewOrExistingVariantMedia(ctx, newMedia, links); err != nil {
				logger.L(ctx).Error("bulk update variants failed", zap.Error(err), zap.String("variant_id", item.ID.String()))
				return nil, err
			}
		}
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

// buildVariantStockRows builds the InventoryItem, ADJUST StockMove and initial
// InventoryLevel rows for a single new variant, mirroring the product-create
// workflow in insertUseCase.go.
func buildVariantStockRows(variantID uuid.UUID, trackInventory *bool, stock int) (domain.InventoryItem, domain.StockMove, domain.InventoryLevel) {
	inventoryItemID := uuid.Must(uuid.NewV7())

	track := true
	if trackInventory != nil {
		track = *trackInventory
	}

	item := domain.InventoryItem{
		ID:             inventoryItemID,
		VariantID:      &variantID,
		TrackInventory: track,
	}

	move := domain.StockMove{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: inventoryItemID,
		MoveType:        domain.StockMoveAdjust,
		Quantity:        stock,
	}

	level := domain.InventoryLevel{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: inventoryItemID,
		AvailableQty:    stock,
		ReservedQty:     0,
	}

	return item, move, level
}

// resolveVariantMedia resolves a variant's media items into the rows to persist:
// brand-new ProductMedia rows plus VariantMedia links (covering both new and
// existing media). Existing media ids are ownership-checked against the
// variant's product.
func (uc *variantUc) resolveVariantMedia(ctx context.Context, productID, variantID uuid.UUID, items []domain.VariantMediaItemInput) ([]domain.ProductMedia, []domain.VariantMedia, error) {

	newMedia := make([]domain.ProductMedia, 0, len(items))
	links := make([]domain.VariantMedia, 0, len(items))

	for _, item := range items {
		if item.ID != nil {
			media, err := uc.productRepo.FindMediaByID(ctx, *item.ID)
			if err != nil {
				return nil, nil, err
			}
			if media.ProductID != productID {
				return nil, nil, domain.ErrMediaNotFound
			}
			links = append(links, domain.VariantMedia{
				VariantID: variantID,
				MediaID:   *item.ID,
				Position:  item.Position,
			})
			continue
		}

		mediaID := uuid.Must(uuid.NewV7())
		newMedia = append(newMedia, domain.ProductMedia{
			ID:        mediaID,
			ProductID: productID,
			Type:      item.Type,
			URL:       item.URL,
			AltText:   item.AltText,
			Position:  item.Position,
		})
		links = append(links, domain.VariantMedia{
			VariantID: variantID,
			MediaID:   mediaID,
			Position:  item.Position,
		})
	}

	return newMedia, links, nil
}

// validateCreateVariantInput performs use-case level validation for a single
// variant create that cannot be expressed with struct tags (or is conditional).
func validateCreateVariantInput(input domain.CreateVariantInput) error {
	if input.Price == nil || input.Weight == nil {
		return domain.ErrProductInvalidInput
	}
	if input.Stock < 0 {
		return domain.ErrProductInvalidInput
	}
	return validateVariantMediaInput(input.Media)
}

// validateVariantMediaInput checks the conditional rule: brand-new media items
// (no id) must provide both a type and a url.
func validateVariantMediaInput(items []domain.VariantMediaItemInput) error {
	for _, item := range items {
		if item.ID == nil {
			if strings.TrimSpace(item.Type) == "" || strings.TrimSpace(item.URL) == "" {
				return domain.ErrProductInvalidInput
			}
		}
	}
	return nil
}
