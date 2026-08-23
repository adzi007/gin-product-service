package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestToProductDetailData_CategoryAndStock(t *testing.T) {
	p := domain.ProductDetail{
		Product: domain.Product{
			ID:     uuid.New(),
			Handle: "ergonomic-cotton-hoodie",
			Title:  "Ergonomic Cotton Hoodie",
			Status: domain.ProductStatusActive,
			Variants: []domain.Variant{
				{
					ID:    uuid.New(),
					SKU:   strPtr("HOODIE-BLK-M"),
					Price: decimal.NewFromFloat(59.99),
					// Stock:   decimal.NewFromInt(25),
					Stock:   25,
					Options: []byte(`[{"option":"Color","value":"Black"}]`),
				},
			},
		},
		Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
	}

	data := toProductDetailData(p)

	if data.Category.Slug != "apparel" {
		t.Errorf("expected category slug apparel, got %q", data.Category.Slug)
	}
	if data.Category.Name != "Apparel" {
		t.Errorf("expected category name Apparel, got %q", data.Category.Name)
	}
	if data.Status != domain.ProductStatusActive {
		t.Errorf("expected status active, got %q", data.Status)
	}
	if len(data.Variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(data.Variants))
	}
	// if !data.Variants[0].Stock.Equal(decimal.NewFromInt(25)) {
	// 	t.Errorf("expected variant stock 25, got %s", data.Variants[0].Stock)
	// }
	if data.Variants[0].Stock != 25 {
		t.Errorf("expected variant stock 25, got %d", data.Variants[0].Stock)
	}
}

func TestToProductDetailData_JSONOmitsCategoryID(t *testing.T) {
	p := domain.ProductDetail{
		Product: domain.Product{
			ID:     uuid.New(),
			Handle: "ergonomic-cotton-hoodie",
			Title:  "Ergonomic Cotton Hoodie",
			Status: domain.ProductStatusActive,
			Variants: []domain.Variant{
				{ID: uuid.New(), Price: decimal.NewFromFloat(59.99), Stock: 25, Options: []byte(`[]`)},
			},
		},
		Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
	}

	raw, err := json.Marshal(toProductDetailData(p))
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	s := string(raw)
	if strings.Contains(s, "category_id") {
		t.Errorf("detail response must not contain category_id: %s", s)
	}
	for _, want := range []string{`"category"`, `"slug":"apparel"`, `"name":"Apparel"`, `"stock"`, `"price"`, `"status":"active"`} {
		if !strings.Contains(s, want) {
			t.Errorf("detail response missing %q: %s", want, s)
		}
	}
}

func TestProductListJSON_CategoryAndPrices(t *testing.T) {
	item := domain.ProductListItem{
		Product: domain.Product{
			ID:     uuid.New(),
			Handle: "ergonomic-cotton-hoodie",
			Title:  "Ergonomic Cotton Hoodie",
		},
		Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
		Prices:   domain.ProductPrices{StartPrice: decimal.NewFromFloat(29.99), MaxPrice: decimal.NewFromFloat(59.99)},
	}

	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	s := string(raw)
	if strings.Contains(s, "category_id") {
		t.Errorf("list product must not contain category_id: %s", s)
	}
	for _, want := range []string{`"category"`, `"slug":"apparel"`, `"name":"Apparel"`, `"prices"`, `"startPrice"`, `"maxPrice"`} {
		if !strings.Contains(s, want) {
			t.Errorf("list product missing %q: %s", want, s)
		}
	}
}

func strPtr(s string) *string {
	return &s
}

// fakeQueryProductUseCase is a minimal QueryProductUseCase fake for handler tests.
type fakeQueryProductUseCase struct {
	findAllParams domain.ListProductParams
	findAllResult domain.PaginatedProducts
	findAllErr    error
}

func (f *fakeQueryProductUseCase) FindAll(ctx context.Context, params domain.ListProductParams) (domain.PaginatedProducts, error) {
	f.findAllParams = params
	return f.findAllResult, f.findAllErr
}

func (f *fakeQueryProductUseCase) GetByID(ctx context.Context, id uuid.UUID) (domain.ProductDetail, error) {
	return domain.ProductDetail{}, nil
}

func (f *fakeQueryProductUseCase) GetByHandle(ctx context.Context, handle string) (domain.ProductDetail, error) {
	return domain.ProductDetail{}, nil
}

func TestProductHandler_Fetch_ParsesFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	queryUC := &fakeQueryProductUseCase{
		findAllResult: domain.PaginatedProducts{Data: []domain.ProductListItem{}},
	}
	h := NewProductHandler(nil, queryUC, nil, nil, nil, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/products?search=hood&category_id=7&status=active&page=2&per_page=5&sort_by=category_name&sort_dir=asc", nil)

	h.Fetch(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	p := queryUC.findAllParams
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

func TestProductHandler_Fetch_RejectsInvalidStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	queryUC := &fakeQueryProductUseCase{}
	h := NewProductHandler(nil, queryUC, nil, nil, nil, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/products?status=bogus", nil)

	h.Fetch(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// fakeVariantUseCase is a minimal VariantUseCase fake for handler tests.
type fakeVariantUseCase struct {
	createErr    error
	createResult domain.Variant
	updateErr    error
	deleteErr    error
	restoreErr   error
}

func (f *fakeVariantUseCase) Create(ctx context.Context, productID uuid.UUID, input domain.CreateVariantInput) (domain.Variant, error) {
	if f.createErr != nil {
		return domain.Variant{}, f.createErr
	}
	return f.createResult, nil
}

func (f *fakeVariantUseCase) BulkCreate(ctx context.Context, productID uuid.UUID, input domain.BulkCreateVariantsInput) ([]domain.Variant, error) {
	return nil, nil
}

func (f *fakeVariantUseCase) Update(ctx context.Context, variantID uuid.UUID, input domain.UpdateVariantInput) (domain.Variant, error) {
	if f.updateErr != nil {
		return domain.Variant{}, f.updateErr
	}
	return domain.Variant{ID: variantID}, nil
}

func (f *fakeVariantUseCase) BulkUpdate(ctx context.Context, productID uuid.UUID, input domain.BulkUpdateVariantsInput) ([]domain.Variant, error) {
	return nil, nil
}

func (f *fakeVariantUseCase) Delete(ctx context.Context, variantID uuid.UUID) error {
	return f.deleteErr
}

func (f *fakeVariantUseCase) BulkDelete(ctx context.Context, productID uuid.UUID, input domain.BulkDeleteVariantsInput) error {
	return nil
}

func (f *fakeVariantUseCase) Restore(ctx context.Context, variantID uuid.UUID) (domain.Variant, error) {
	if f.restoreErr != nil {
		return domain.Variant{}, f.restoreErr
	}
	return domain.Variant{ID: variantID}, nil
}

func (f *fakeVariantUseCase) Reorder(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {
	return nil
}

// fakeMediaUseCase is a minimal MediaUseCase fake for handler tests.
type fakeMediaUseCase struct {
	attachErr    error
	attachResult domain.VariantMedia
}

func (f *fakeMediaUseCase) Create(ctx context.Context, productID uuid.UUID, input domain.BulkCreateMediaInput) ([]domain.ProductMedia, error) {
	return nil, nil
}

func (f *fakeMediaUseCase) Update(ctx context.Context, productID, mediaID uuid.UUID, input domain.UpdateMediaInput) (domain.ProductMedia, error) {
	return domain.ProductMedia{}, nil
}

func (f *fakeMediaUseCase) Delete(ctx context.Context, productID, mediaID uuid.UUID) error {
	return nil
}

func (f *fakeMediaUseCase) Reorder(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {
	return nil
}

func (f *fakeMediaUseCase) AttachToVariant(ctx context.Context, variantID uuid.UUID, input domain.AttachVariantMediaInput) (domain.VariantMedia, error) {
	if f.attachErr != nil {
		return domain.VariantMedia{}, f.attachErr
	}
	return f.attachResult, nil
}

func (f *fakeMediaUseCase) DetachFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) error {
	return nil
}

func (f *fakeMediaUseCase) ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []domain.PositionUpdate) error {
	return nil
}

func TestProductHandler_CreateVariant_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	id := uuid.New()
	variantUC := &fakeVariantUseCase{
		createResult: domain.Variant{ID: id, Price: decimal.NewFromFloat(19.99), Weight: decimal.NewFromFloat(0.5)},
	}
	h := NewProductHandler(nil, nil, nil, nil, nil, variantUC, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id.String()}}
	c.Request = httptest.NewRequest(http.MethodPost, "/products/"+id.String()+"/variants", strings.NewReader(`{"price": 19.99, "weight": 0.5}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateVariant(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProductHandler_CreateVariant_InvalidProductID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewProductHandler(nil, nil, nil, nil, nil, &fakeVariantUseCase{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/products/not-a-uuid/variants", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateVariant(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProductHandler_DeleteVariant_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	id := uuid.New()
	variantUC := &fakeVariantUseCase{deleteErr: domain.ErrVariantNotFound}
	h := NewProductHandler(nil, nil, nil, nil, nil, variantUC, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id.String()}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/variants/"+id.String(), nil)

	h.DeleteVariant(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProductHandler_AttachVariantMedia_InvalidVariantID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewProductHandler(nil, nil, nil, nil, nil, nil, &fakeMediaUseCase{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/variants/not-a-uuid/media", strings.NewReader(`{"media_id": "`+uuid.New().String()+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.AttachVariantMedia(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
