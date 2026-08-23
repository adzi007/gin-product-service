package product

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type fakeProductRepo struct {
	findAllParams domain.ListProductParams
	findAllData   []domain.ProductListItem
	findAllTotal  int
	findAllErr    error

	findByIDData    domain.ProductDetail
	findByIDErr     error
	findByHandleErr error

	createParams domain.CreateProductParams

	updateHeaderID     uuid.UUID
	updateHeaderInput  domain.UpdateProductInput
	updateHeaderResult domain.Product
	updateHeaderErr    error

	updateStatusID     uuid.UUID
	updateStatusStatus domain.ProductStatus
	updateStatusErr    error

	deleteID  uuid.UUID
	deleteErr error

	createOption    domain.ProductOption
	createOptionErr error

	renameOptionProductID uuid.UUID
	renameOptionID        uuid.UUID
	renameOptionName      string
	renameOptionErr       error

	deleteOptionProductID uuid.UUID
	deleteOptionID        uuid.UUID
	deleteOptionErr       error

	reorderProductID uuid.UUID
	reorderPositions []domain.PositionUpdate
	reorderErr       error

	findOptionByIDErr error

	createOptionValue    domain.ProductOptionValue
	createOptionValueErr error

	updateOptionValueID       uuid.UUID
	updateOptionValueValue    *string
	updateOptionValuePosition *int
	updateOptionValueErr      error

	deleteOptionValueID  uuid.UUID
	deleteOptionValueErr error

	findOptionValueByIDErr error

	createVariant    domain.Variant
	createVariantErr error

	createVariants    []domain.Variant
	createVariantsErr error

	createVariantsWithStockParams domain.CreateVariantsParams
	createVariantsWithStockErr    error

	adjustVariantStockVariantID uuid.UUID
	adjustVariantStockTargetQty domain.Quantity
	adjustVariantStockResult    domain.InventoryLevel
	adjustVariantStockErr       error

	findVariantByIDData domain.Variant
	findVariantByIDErr  error

	updateVariantID    uuid.UUID
	updateVariantInput domain.UpdateVariantInput
	updateVariantErr   error

	deleteVariantID   uuid.UUID
	deleteVariantHard bool
	deleteVariantErr  error

	bulkDeleteVariantIDs  []uuid.UUID
	bulkDeleteVariantHard bool
	bulkDeleteVariantsErr error

	restoreVariantID  uuid.UUID
	restoreVariantErr error

	reorderVariantsProductID uuid.UUID
	reorderVariantsPositions []domain.PositionUpdate
	reorderVariantsErr       error

	variantHasHistoryID     uuid.UUID
	variantHasHistoryResult bool
	variantHasHistoryErr    error

	createProductMedia    []domain.ProductMedia
	createProductMediaErr error

	updateProductMediaID      uuid.UUID
	updateProductMediaAltText *string
	updateProductMediaErr     error

	deleteProductMediaID  uuid.UUID
	deleteProductMediaErr error

	reorderProductMediaProductID uuid.UUID
	reorderProductMediaPositions []domain.PositionUpdate
	reorderProductMediaErr       error

	findMediaByIDData domain.ProductMedia
	findMediaByIDErr  error

	attachVariantMediaVariantID uuid.UUID
	attachVariantMediaMediaID   uuid.UUID
	attachVariantMediaErr       error

	attachNewOrExistingVariantMediaNewMedia []domain.ProductMedia
	attachNewOrExistingVariantMediaLinks    []domain.VariantMedia
	attachNewOrExistingVariantMediaErr      error

	detachVariantMediaVariantID uuid.UUID
	detachVariantMediaMediaID   uuid.UUID
	detachVariantMediaErr       error

	reorderVariantMediaVariantID uuid.UUID
	reorderVariantMediaPositions []domain.PositionUpdate
	reorderVariantMediaErr       error
}

func (f *fakeProductRepo) Create(ctx context.Context, params domain.CreateProductParams) (domain.Product, error) {
	f.createParams = params
	return domain.Product{}, nil
}

func (f *fakeProductRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.ProductListItem, int, error) {
	f.findAllParams = params
	return f.findAllData, f.findAllTotal, f.findAllErr
}

func (f *fakeProductRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.ProductDetail, error) {
	if f.findByIDErr != nil {
		return domain.ProductDetail{}, f.findByIDErr
	}
	if f.findByIDData.ID != uuid.Nil {
		return f.findByIDData, nil
	}
	return domain.ProductDetail{Product: domain.Product{ID: id}}, nil
}

func (f *fakeProductRepo) FindByHandle(ctx context.Context, handle string) (domain.ProductDetail, error) {
	if f.findByHandleErr != nil {
		return domain.ProductDetail{}, f.findByHandleErr
	}
	return domain.ProductDetail{Product: domain.Product{Handle: handle}}, nil
}

func (f *fakeProductRepo) UpdateHeader(ctx context.Context, id uuid.UUID, input domain.UpdateProductInput) (domain.Product, error) {
	f.updateHeaderID = id
	f.updateHeaderInput = input
	if f.updateHeaderErr != nil {
		return domain.Product{}, f.updateHeaderErr
	}
	return f.updateHeaderResult, nil
}

func (f *fakeProductRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.ProductStatus) error {
	f.updateStatusID = id
	f.updateStatusStatus = status
	return f.updateStatusErr
}

func (f *fakeProductRepo) Delete(ctx context.Context, id uuid.UUID) error {
	f.deleteID = id
	return f.deleteErr
}

func (f *fakeProductRepo) CreateOption(ctx context.Context, option domain.ProductOption) (domain.ProductOption, error) {
	f.createOption = option
	if f.createOptionErr != nil {
		return domain.ProductOption{}, f.createOptionErr
	}
	return option, nil
}

func (f *fakeProductRepo) RenameOption(ctx context.Context, productID, optionID uuid.UUID, name string) (domain.ProductOption, error) {
	f.renameOptionProductID = productID
	f.renameOptionID = optionID
	f.renameOptionName = name
	if f.renameOptionErr != nil {
		return domain.ProductOption{}, f.renameOptionErr
	}
	return domain.ProductOption{ID: optionID, ProductID: productID, Name: name}, nil
}

func (f *fakeProductRepo) DeleteOption(ctx context.Context, productID, optionID uuid.UUID) error {
	f.deleteOptionProductID = productID
	f.deleteOptionID = optionID
	return f.deleteOptionErr
}

func (f *fakeProductRepo) ReorderOptions(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {
	f.reorderProductID = productID
	f.reorderPositions = positions
	return f.reorderErr
}

func (f *fakeProductRepo) FindOptionByID(ctx context.Context, optionID uuid.UUID) (domain.ProductOption, error) {
	if f.findOptionByIDErr != nil {
		return domain.ProductOption{}, f.findOptionByIDErr
	}
	return domain.ProductOption{ID: optionID}, nil
}

func (f *fakeProductRepo) CreateOptionValue(ctx context.Context, value domain.ProductOptionValue) (domain.ProductOptionValue, error) {
	f.createOptionValue = value
	if f.createOptionValueErr != nil {
		return domain.ProductOptionValue{}, f.createOptionValueErr
	}
	return value, nil
}

func (f *fakeProductRepo) UpdateOptionValue(ctx context.Context, valueID uuid.UUID, value *string, position *int) (domain.ProductOptionValue, error) {
	f.updateOptionValueID = valueID
	f.updateOptionValueValue = value
	f.updateOptionValuePosition = position
	if f.updateOptionValueErr != nil {
		return domain.ProductOptionValue{}, f.updateOptionValueErr
	}
	return domain.ProductOptionValue{ID: valueID}, nil
}

func (f *fakeProductRepo) DeleteOptionValue(ctx context.Context, valueID uuid.UUID) error {
	f.deleteOptionValueID = valueID
	return f.deleteOptionValueErr
}

func (f *fakeProductRepo) FindOptionValueByID(ctx context.Context, valueID uuid.UUID) (domain.ProductOptionValue, error) {
	if f.findOptionValueByIDErr != nil {
		return domain.ProductOptionValue{}, f.findOptionValueByIDErr
	}
	return domain.ProductOptionValue{ID: valueID}, nil
}

func (f *fakeProductRepo) CreateVariant(ctx context.Context, variant domain.Variant) (domain.Variant, error) {
	f.createVariant = variant
	if f.createVariantErr != nil {
		return domain.Variant{}, f.createVariantErr
	}
	return variant, nil
}

func (f *fakeProductRepo) CreateVariants(ctx context.Context, variants []domain.Variant) ([]domain.Variant, error) {
	f.createVariants = variants
	if f.createVariantsErr != nil {
		return nil, f.createVariantsErr
	}
	return variants, nil
}

func (f *fakeProductRepo) CreateVariantsWithStock(ctx context.Context, params domain.CreateVariantsParams) ([]domain.Variant, error) {
	f.createVariantsWithStockParams = params
	if f.createVariantsWithStockErr != nil {
		return nil, f.createVariantsWithStockErr
	}
	return params.Variants, nil
}

func (f *fakeProductRepo) AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty domain.Quantity) (domain.InventoryLevel, error) {
	f.adjustVariantStockVariantID = variantID
	f.adjustVariantStockTargetQty = targetQty
	if f.adjustVariantStockErr != nil {
		return domain.InventoryLevel{}, f.adjustVariantStockErr
	}
	if f.adjustVariantStockResult.ID != uuid.Nil {
		return f.adjustVariantStockResult, nil
	}
	return domain.InventoryLevel{InventoryItemID: uuid.New(), AvailableQty: targetQty}, nil
}

func (f *fakeProductRepo) FindVariantByID(ctx context.Context, variantID uuid.UUID) (domain.Variant, error) {
	if f.findVariantByIDErr != nil {
		return domain.Variant{}, f.findVariantByIDErr
	}
	if f.findVariantByIDData.ID != uuid.Nil {
		return f.findVariantByIDData, nil
	}
	return domain.Variant{ID: variantID}, nil
}

func (f *fakeProductRepo) UpdateVariant(ctx context.Context, variantID uuid.UUID, input domain.UpdateVariantInput) (domain.Variant, error) {
	f.updateVariantID = variantID
	f.updateVariantInput = input
	if f.updateVariantErr != nil {
		return domain.Variant{}, f.updateVariantErr
	}
	return domain.Variant{ID: variantID}, nil
}

func (f *fakeProductRepo) DeleteVariant(ctx context.Context, variantID uuid.UUID, hard bool) error {
	f.deleteVariantID = variantID
	f.deleteVariantHard = hard
	return f.deleteVariantErr
}

func (f *fakeProductRepo) BulkDeleteVariants(ctx context.Context, variantIDs []uuid.UUID, hard bool) error {
	f.bulkDeleteVariantIDs = variantIDs
	f.bulkDeleteVariantHard = hard
	return f.bulkDeleteVariantsErr
}

func (f *fakeProductRepo) RestoreVariant(ctx context.Context, variantID uuid.UUID) (domain.Variant, error) {
	f.restoreVariantID = variantID
	if f.restoreVariantErr != nil {
		return domain.Variant{}, f.restoreVariantErr
	}
	return domain.Variant{ID: variantID}, nil
}

func (f *fakeProductRepo) ReorderVariants(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {
	f.reorderVariantsProductID = productID
	f.reorderVariantsPositions = positions
	return f.reorderVariantsErr
}

func (f *fakeProductRepo) VariantHasHistory(ctx context.Context, variantID uuid.UUID) (bool, error) {
	f.variantHasHistoryID = variantID
	return f.variantHasHistoryResult, f.variantHasHistoryErr
}

func (f *fakeProductRepo) CreateProductMedia(ctx context.Context, media []domain.ProductMedia) ([]domain.ProductMedia, error) {
	f.createProductMedia = media
	if f.createProductMediaErr != nil {
		return nil, f.createProductMediaErr
	}
	return media, nil
}

func (f *fakeProductRepo) UpdateProductMedia(ctx context.Context, mediaID uuid.UUID, altText *string) (domain.ProductMedia, error) {
	f.updateProductMediaID = mediaID
	f.updateProductMediaAltText = altText
	if f.updateProductMediaErr != nil {
		return domain.ProductMedia{}, f.updateProductMediaErr
	}
	return domain.ProductMedia{ID: mediaID, AltText: altText}, nil
}

func (f *fakeProductRepo) DeleteProductMedia(ctx context.Context, mediaID uuid.UUID) error {
	f.deleteProductMediaID = mediaID
	return f.deleteProductMediaErr
}

func (f *fakeProductRepo) ReorderProductMedia(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {
	f.reorderProductMediaProductID = productID
	f.reorderProductMediaPositions = positions
	return f.reorderProductMediaErr
}

func (f *fakeProductRepo) FindMediaByID(ctx context.Context, mediaID uuid.UUID) (domain.ProductMedia, error) {
	if f.findMediaByIDErr != nil {
		return domain.ProductMedia{}, f.findMediaByIDErr
	}
	if f.findMediaByIDData.ID != uuid.Nil {
		return f.findMediaByIDData, nil
	}
	return domain.ProductMedia{ID: mediaID}, nil
}

func (f *fakeProductRepo) AttachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) (domain.VariantMedia, error) {
	f.attachVariantMediaVariantID = variantID
	f.attachVariantMediaMediaID = mediaID
	if f.attachVariantMediaErr != nil {
		return domain.VariantMedia{}, f.attachVariantMediaErr
	}
	return domain.VariantMedia{VariantID: variantID, MediaID: mediaID}, nil
}

func (f *fakeProductRepo) AttachNewOrExistingVariantMedia(ctx context.Context, newMedia []domain.ProductMedia, links []domain.VariantMedia) error {
	f.attachNewOrExistingVariantMediaNewMedia = newMedia
	f.attachNewOrExistingVariantMediaLinks = links
	return f.attachNewOrExistingVariantMediaErr
}

func (f *fakeProductRepo) DetachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) error {
	f.detachVariantMediaVariantID = variantID
	f.detachVariantMediaMediaID = mediaID
	return f.detachVariantMediaErr
}

func (f *fakeProductRepo) ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []domain.PositionUpdate) error {
	f.reorderVariantMediaVariantID = variantID
	f.reorderVariantMediaPositions = positions
	return f.reorderVariantMediaErr
}

var (
	_ domain.ProductRepository = (*fakeProductRepo)(nil)
	_ domain.OptionRepository  = (*fakeProductRepo)(nil)
	_ domain.VariantRepository = (*fakeProductRepo)(nil)
	_ domain.MediaRepository   = (*fakeProductRepo)(nil)
)
var _ domain.QueryProductUseCase = (*queryProductUc)(nil)

func TestQueryProductUseCase_FindAll_AppliesDefaults(t *testing.T) {
	repo := &fakeProductRepo{
		findAllData:  []domain.ProductListItem{{}},
		findAllTotal: 1,
	}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.FindAll(context.Background(), domain.ListProductParams{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if repo.findAllParams.Page != 1 {
		t.Errorf("expected default Page 1, got %d", repo.findAllParams.Page)
	}
	if repo.findAllParams.PerPage != 10 {
		t.Errorf("expected default PerPage 10, got %d", repo.findAllParams.PerPage)
	}
	if repo.findAllParams.SortBy != "created_at" {
		t.Errorf("expected default SortBy created_at, got %q", repo.findAllParams.SortBy)
	}
	if repo.findAllParams.SortDir != "desc" {
		t.Errorf("expected default SortDir desc, got %q", repo.findAllParams.SortDir)
	}
}

func TestQueryProductUseCase_FindAll_ComputesTotalPages(t *testing.T) {
	repo := &fakeProductRepo{
		findAllData:  []domain.ProductListItem{{}, {}},
		findAllTotal: 21,
	}
	uc := NewProductQueryUseCase(repo)

	result, err := uc.FindAll(context.Background(), domain.ListProductParams{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.TotalPages != 3 {
		t.Errorf("expected TotalPages 3, got %d", result.TotalPages)
	}
	if len(result.Data) != 2 {
		t.Errorf("expected 2 data items, got %d", len(result.Data))
	}
}

func TestQueryProductUseCase_GetByID_NotFound(t *testing.T) {
	repo := &fakeProductRepo{findByIDErr: domain.ErrProductNotFound}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.GetByID(context.Background(), uuid.New())
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestQueryProductUseCase_GetByHandle_NotFound(t *testing.T) {
	repo := &fakeProductRepo{findByHandleErr: domain.ErrProductNotFound}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.GetByHandle(context.Background(), "does-not-exist")
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestQueryProductUseCase_GetByID_ReturnsProduct(t *testing.T) {
	id := uuid.New()
	repo := &fakeProductRepo{}
	uc := NewProductQueryUseCase(repo)

	got, err := uc.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.ID != id {
		t.Errorf("expected product ID %s, got %s", id, got.ID)
	}
}

func TestQueryProductUseCase_GetByHandle_ReturnsProduct(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductQueryUseCase(repo)

	got, err := uc.GetByHandle(context.Background(), "tshirt-basic")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.Handle != "tshirt-basic" {
		t.Errorf("expected handle tshirt-basic, got %q", got.Handle)
	}
}

func TestQueryProductUseCase_FindAll_PassesFiltersThrough(t *testing.T) {
	repo := &fakeProductRepo{findAllData: []domain.ProductListItem{{}}}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.FindAll(context.Background(), domain.ListProductParams{
		Search:     "hood",
		CategoryID: 7,
		Status:     "active",
		Page:       2,
		PerPage:    5,
		SortBy:     "category_name",
		SortDir:    "asc",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	p := repo.findAllParams
	if p.Search != "hood" {
		t.Errorf("expected Search hood, got %q", p.Search)
	}
	if p.CategoryID != 7 {
		t.Errorf("expected CategoryID 7, got %d", p.CategoryID)
	}
	if p.Status != "active" {
		t.Errorf("expected Status active, got %q", p.Status)
	}
	if p.Page != 2 {
		t.Errorf("expected Page 2, got %d", p.Page)
	}
	if p.PerPage != 5 {
		t.Errorf("expected PerPage 5, got %d", p.PerPage)
	}
	if p.SortBy != "category_name" {
		t.Errorf("expected SortBy category_name, got %q", p.SortBy)
	}
	if p.SortDir != "asc" {
		t.Errorf("expected SortDir asc, got %q", p.SortDir)
	}
}

func TestQueryProductUseCase_FindAll_RejectsInvalidStatus(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.FindAll(context.Background(), domain.ListProductParams{Status: "bogus"})
	if err != domain.ErrProductInvalidStatus {
		t.Fatalf("expected ErrProductInvalidStatus, got %v", err)
	}
}

func TestProductInsertUseCase_Create_DefaultsStatusToDraft(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductInsertUseCase(repo)

	price := decimal.NewFromInt(100)
	weight := decimal.NewFromInt(1)

	_, err := uc.Create(context.Background(), domain.CreateProductInput{
		Handle:     "test-product",
		Title:      "Test Product",
		CategoryID: 1,
		Variants: []domain.VariantInput{
			{Price: &price, Weight: &weight, Stock: 0},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.createParams.Product.Status != domain.ProductStatusDraft {
		t.Errorf("expected default status draft, got %q", repo.createParams.Product.Status)
	}
}

func TestProductInsertUseCase_Create_PassesThroughProvidedStatus(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductInsertUseCase(repo)

	price := decimal.NewFromInt(100)
	weight := decimal.NewFromInt(1)
	status := domain.ProductStatusActive

	_, err := uc.Create(context.Background(), domain.CreateProductInput{
		Handle:     "test-product",
		Title:      "Test Product",
		CategoryID: 1,
		Status:     &status,
		Variants: []domain.VariantInput{
			{Price: &price, Weight: &weight, Stock: 0},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.createParams.Product.Status != domain.ProductStatusActive {
		t.Errorf("expected status active, got %q", repo.createParams.Product.Status)
	}
}

func TestProductInsertUseCase_Create_RejectsInvalidStatus(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductInsertUseCase(repo)

	price := decimal.NewFromInt(100)
	weight := decimal.NewFromInt(1)
	badStatus := domain.ProductStatus("bogus")

	_, err := uc.Create(context.Background(), domain.CreateProductInput{
		Handle:     "test-product",
		Title:      "Test Product",
		CategoryID: 1,
		Status:     &badStatus,
		Variants: []domain.VariantInput{
			{Price: &price, Weight: &weight, Stock: 0},
		},
	})
	if err != domain.ErrProductInvalidStatus {
		t.Fatalf("expected ErrProductInvalidStatus, got %v", err)
	}
}
