package product

import (
	"context"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type mediaUc struct {
	productRepo domain.ProductRepository
}

func NewMediaUseCase(productRepo domain.ProductRepository) domain.MediaUseCase {
	return &mediaUc{
		productRepo: productRepo,
	}
}

func (uc *mediaUc) Create(ctx context.Context, productID uuid.UUID, input domain.BulkCreateMediaInput) ([]domain.ProductMedia, error) {

	// Resolve the product to confirm it exists and compute the next media
	// positions from the number of already-existing gallery items.
	product, err := uc.productRepo.FindByID(ctx, productID)
	if err != nil {
		logger.L(ctx).Error("create media failed", zap.Error(err), zap.String("product_id", productID.String()))
		return nil, err
	}

	media := make([]domain.ProductMedia, 0, len(input.Media))
	for i, m := range input.Media {
		media = append(media, domain.ProductMedia{
			ID:        uuid.Must(uuid.NewV7()),
			ProductID: productID,
			Type:      m.Type,
			URL:       m.URL,
			AltText:   m.AltText,
			Position:  len(product.Media) + i,
		})
	}

	created, err := uc.productRepo.CreateProductMedia(ctx, media)
	if err != nil {
		logger.L(ctx).Error("create media failed", zap.Error(err), zap.String("product_id", productID.String()))
		return nil, err
	}

	logger.L(ctx).Info("[INFO] Success create media", zap.String("product_id", productID.String()), zap.Int("count", len(created)))

	return created, nil
}

func (uc *mediaUc) Update(ctx context.Context, productID, mediaID uuid.UUID, input domain.UpdateMediaInput) (domain.ProductMedia, error) {

	if err := uc.ensureMediaBelongsToProduct(ctx, productID, mediaID); err != nil {
		return domain.ProductMedia{}, err
	}

	updated, err := uc.productRepo.UpdateProductMedia(ctx, mediaID, input.AltText)
	if err != nil {
		logger.L(ctx).Error("update media failed", zap.Error(err), zap.String("media_id", mediaID.String()))
		return domain.ProductMedia{}, err
	}

	logger.L(ctx).Info("[INFO] Success update media", zap.String("media_id", updated.ID.String()))

	return updated, nil
}

func (uc *mediaUc) Delete(ctx context.Context, productID, mediaID uuid.UUID) error {

	if err := uc.ensureMediaBelongsToProduct(ctx, productID, mediaID); err != nil {
		return err
	}

	err := uc.productRepo.DeleteProductMedia(ctx, mediaID)
	if err != nil {
		logger.L(ctx).Error("delete media failed", zap.Error(err), zap.String("media_id", mediaID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success delete media", zap.String("media_id", mediaID.String()))

	return nil
}

func (uc *mediaUc) Reorder(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {

	err := uc.productRepo.ReorderProductMedia(ctx, productID, positions)
	if err != nil {
		logger.L(ctx).Error("reorder media failed", zap.Error(err), zap.String("product_id", productID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success reorder media", zap.String("product_id", productID.String()))

	return nil
}

func (uc *mediaUc) AttachToVariant(ctx context.Context, variantID uuid.UUID, input domain.AttachVariantMediaInput) (domain.VariantMedia, error) {

	variant, err := uc.productRepo.FindVariantByID(ctx, variantID)
	if err != nil {
		logger.L(ctx).Error("attach media to variant failed", zap.Error(err), zap.String("variant_id", variantID.String()))
		return domain.VariantMedia{}, err
	}

	media, err := uc.productRepo.FindMediaByID(ctx, input.MediaID)
	if err != nil {
		logger.L(ctx).Error("attach media to variant failed", zap.Error(err), zap.String("media_id", input.MediaID.String()))
		return domain.VariantMedia{}, err
	}

	// Ownership check: the media must belong to the variant's parent product.
	if media.ProductID != variant.ProductID {
		logger.L(ctx).Error("attach media to variant failed", zap.Error(domain.ErrMediaNotFound), zap.String("variant_id", variantID.String()), zap.String("media_id", input.MediaID.String()))
		return domain.VariantMedia{}, domain.ErrMediaNotFound
	}

	link, err := uc.productRepo.AttachVariantMedia(ctx, variantID, input.MediaID)
	if err != nil {
		logger.L(ctx).Error("attach media to variant failed", zap.Error(err), zap.String("variant_id", variantID.String()), zap.String("media_id", input.MediaID.String()))
		return domain.VariantMedia{}, err
	}

	logger.L(ctx).Info("[INFO] Success attach media to variant", zap.String("variant_id", variantID.String()), zap.String("media_id", input.MediaID.String()))

	return link, nil
}

func (uc *mediaUc) DetachFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) error {

	err := uc.productRepo.DetachVariantMedia(ctx, variantID, mediaID)
	if err != nil {
		logger.L(ctx).Error("detach media from variant failed", zap.Error(err), zap.String("variant_id", variantID.String()), zap.String("media_id", mediaID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success detach media from variant", zap.String("variant_id", variantID.String()), zap.String("media_id", mediaID.String()))

	return nil
}

func (uc *mediaUc) ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []domain.PositionUpdate) error {

	err := uc.productRepo.ReorderVariantMedia(ctx, variantID, positions)
	if err != nil {
		logger.L(ctx).Error("reorder variant media failed", zap.Error(err), zap.String("variant_id", variantID.String()))
		return err
	}

	logger.L(ctx).Info("[INFO] Success reorder variant media", zap.String("variant_id", variantID.String()))

	return nil
}

// ensureMediaBelongsToProduct verifies that a media row belongs to the given
// product, returning domain.ErrMediaNotFound on a mismatch to avoid leaking or
// mutating unrelated media across products.
func (uc *mediaUc) ensureMediaBelongsToProduct(ctx context.Context, productID, mediaID uuid.UUID) error {

	media, err := uc.productRepo.FindMediaByID(ctx, mediaID)
	if err != nil {
		logger.L(ctx).Error("ensure media belongs to product failed", zap.Error(err), zap.String("media_id", mediaID.String()))
		return err
	}
	if media.ProductID != productID {
		logger.L(ctx).Error("ensure media belongs to product failed", zap.Error(domain.ErrMediaNotFound), zap.String("product_id", productID.String()), zap.String("media_id", mediaID.String()))
		return domain.ErrMediaNotFound
	}

	return nil
}
