package product

import (
	"context"
	"encoding/json"
	"strings"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type insertProductUc struct {
	productRepo domain.ProductRepository
}

func NewProductInsertUseCase(productRepo domain.ProductRepository) domain.InsertProductUseCase {
	return &insertProductUc{
		productRepo: productRepo,
	}
}

func (uc *insertProductUc) Create(ctx context.Context, input domain.CreateProductInput) (domain.Product, error) {

	if err := validateCreateInput(input); err != nil {
		return domain.Product{}, err
	}

	productID := uuid.Must(uuid.NewV7())

	// Build product options and option values, remembering the generated IDs so
	// variant options and repository inserts can reference them.
	options := make([]domain.ProductOption, 0, len(input.Options))
	valueIDByOptionValue := make(map[string]map[string]uuid.UUID, len(input.Options))

	for i, opt := range input.Options {
		optionID := uuid.Must(uuid.NewV7())

		po := domain.ProductOption{
			ID:        optionID,
			ProductID: productID,
			Name:      opt.Name,
			Position:  i,
			Values:    make([]domain.ProductOptionValue, 0, len(opt.Values)),
		}

		valueIDs := make(map[string]uuid.UUID, len(opt.Values))
		for j, value := range opt.Values {
			valueID := uuid.Must(uuid.NewV7())
			po.Values = append(po.Values, domain.ProductOptionValue{
				ID:       valueID,
				OptionID: optionID,
				Value:    value,
				Position: j,
			})
			valueIDs[value] = valueID
		}

		options = append(options, po)
		valueIDByOptionValue[opt.Name] = valueIDs
	}

	// Insert gallery into product_media and build the temp-key -> real-ID map
	// used to resolve variants[].media[].id references.
	media := make([]domain.ProductMedia, 0, len(input.Gallery))
	mediaIDByTempKey := make(map[string]uuid.UUID, len(input.Gallery))
	for _, g := range input.Gallery {
		mediaID := uuid.Must(uuid.NewV7())
		mediaIDByTempKey[g.ID] = mediaID
		media = append(media, domain.ProductMedia{
			ID:        mediaID,
			ProductID: productID,
			Type:      g.Type,
			URL:       g.URL,
			AltText:   g.AltText,
			Position:  g.Position,
		})
	}

	variants := make([]domain.Variant, 0, len(input.Variants))
	inventoryItems := make([]domain.InventoryItem, 0, len(input.Variants))
	stockMoves := make([]domain.StockMove, 0, len(input.Variants))
	inventoryLevels := make([]domain.InventoryLevel, 0, len(input.Variants))

	for _, v := range input.Variants {
		variantID := uuid.Must(uuid.NewV7())
		inventoryItemID := uuid.Must(uuid.NewV7())

		optsJSON, err := buildVariantOptionsJSON(input.Options, v.Options, valueIDByOptionValue)
		if err != nil {
			return domain.Product{}, err
		}

		variantMedia := make([]domain.VariantMedia, 0, len(v.Media))
		for _, m := range v.Media {
			mediaID, ok := mediaIDByTempKey[m.ID]
			if !ok {
				return domain.Product{}, domain.ErrInvalidMediaAssignment
			}
			variantMedia = append(variantMedia, domain.VariantMedia{
				VariantID: variantID,
				MediaID:   mediaID,
				Position:  m.Position,
			})
		}

		trackInventory := true
		if v.TrackInventory != nil {
			trackInventory = *v.TrackInventory
		}
		// targetQty := decimal.NewFromInt(int64(v.Stock))

		variants = append(variants, domain.Variant{
			ID:        variantID,
			ProductID: productID,
			SKU:       v.SKU,
			Barcode:   v.Barcode,
			Title:     v.Title,
			Price:     *v.Price,
			Weight:    *v.Weight,
			Options:   optsJSON,
			IsDeleted: false,
			Media:     variantMedia,
		})

		inventoryItems = append(inventoryItems, domain.InventoryItem{
			ID:             inventoryItemID,
			VariantID:      &variantID,
			TrackInventory: trackInventory,
		})

		// ADJUST sets available_qty to an absolute target. There is no
		// pre-existing inventory_levels row for a brand-new variant, so the
		// "current" available_qty is 0 and the delta equals +stock.
		// delta := computeAdjustDelta(decimal.Zero, targetQty)
		stockMoves = append(stockMoves, domain.StockMove{
			ID:              uuid.Must(uuid.NewV7()),
			InventoryItemID: inventoryItemID,
			MoveType:        domain.StockMoveAdjust,
			// Quantity:        delta,
			Quantity: v.Stock,
		})

		inventoryLevels = append(inventoryLevels, domain.InventoryLevel{
			ID:              uuid.Must(uuid.NewV7()),
			InventoryItemID: inventoryItemID,
			// AvailableQty:    targetQty,
			AvailableQty: v.Stock,
			// ReservedQty:     decimal.Zero,
			ReservedQty: 0,
		})
	}

	status := domain.ProductStatusDraft
	if input.Status != nil {
		status = *input.Status
	}

	product := domain.Product{
		ID:          productID,
		Handle:      input.Handle,
		Title:       input.Title,
		Status:      status,
		Description: input.Description,
		Vendor:      input.Vendor,
		CategoryID:  input.CategoryID,
		Options:     options,
		Variants:    variants,
		Media:       media,
	}

	created, err := uc.productRepo.Create(ctx, domain.CreateProductParams{
		Product:         product,
		InventoryItems:  inventoryItems,
		StockMoves:      stockMoves,
		InventoryLevels: inventoryLevels,
	})
	if err != nil {
		logger.L(ctx).Error("create product failed", zap.Error(err))
		return domain.Product{}, err
	}

	logger.L(ctx).Info("[INFO] Success create product", zap.String("id", created.ID.String()))

	return created, nil
}

// validateCreateInput performs use-case level validation that cannot be
// expressed (or is more naturally expressed) with struct tags.
func validateCreateInput(input domain.CreateProductInput) error {
	if strings.TrimSpace(input.Handle) == "" || strings.TrimSpace(input.Title) == "" {
		return domain.ErrProductInvalidInput
	}
	if input.CategoryID <= 0 {
		return domain.ErrProductInvalidInput
	}
	if input.Status != nil && !isValidProductStatus(*input.Status) {
		return domain.ErrProductInvalidStatus
	}
	if len(input.Variants) == 0 {
		return domain.ErrProductInvalidInput
	}

	seenOptionNames := make(map[string]struct{}, len(input.Options))
	for _, opt := range input.Options {
		if strings.TrimSpace(opt.Name) == "" || len(opt.Values) == 0 {
			return domain.ErrProductInvalidInput
		}
		if _, dup := seenOptionNames[opt.Name]; dup {
			return domain.ErrProductInvalidInput
		}
		seenOptionNames[opt.Name] = struct{}{}
	}

	seenSKUs := make(map[string]struct{}, len(input.Variants))
	for _, v := range input.Variants {
		if v.Price == nil || v.Weight == nil {
			return domain.ErrProductInvalidInput
		}
		if v.Stock < 0 {
			return domain.ErrProductInvalidInput
		}
		if v.SKU != nil && strings.TrimSpace(*v.SKU) != "" {
			if _, dup := seenSKUs[*v.SKU]; dup {
				return domain.ErrSKUAlreadyExists
			}
			seenSKUs[*v.SKU] = struct{}{}
		}
	}

	return nil
}

// isValidProductStatus reports whether s is one of the allowed product statuses.
func isValidProductStatus(s domain.ProductStatus) bool {
	switch s {
	case domain.ProductStatusDraft, domain.ProductStatusActive, domain.ProductStatusArchived:
		return true
	default:
		return false
	}
}

// buildVariantOptionsJSON produces the variants.options JSONB payload from the
// variant's option map, resolving and validating every key/value against the
// declared product options. Output is ordered by the declared option order so
// the stored JSONB is deterministic.
func buildVariantOptionsJSON(
	declared []domain.ProductOptionInput,
	selected map[string]string,
	valueIDByOptionValue map[string]map[string]uuid.UUID,
) ([]byte, error) {

	// Every selected key must reference a declared option.
	for name := range selected {
		if _, ok := valueIDByOptionValue[name]; !ok {
			return nil, domain.ErrInvalidOption
		}
	}

	opts := make([]domain.VariantOption, 0, len(selected))
	for _, opt := range declared {
		value, ok := selected[opt.Name]
		if !ok {
			continue
		}
		if _, exists := valueIDByOptionValue[opt.Name][value]; !exists {
			return nil, domain.ErrInvalidOption
		}
		opts = append(opts, domain.VariantOption{Option: opt.Name, Value: value})
	}

	return json.Marshal(opts)
}

// computeAdjustDelta calculates the quantity to record for an ADJUST move:
// the difference between the current available quantity and the requested
// absolute target. This is kept generic so it can be reused by the standalone
// stock-move use case.
func computeAdjustDelta(current, target decimal.Decimal) decimal.Decimal {
	return target.Sub(current)
}
