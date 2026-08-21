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
	p := domain.Product{
		ID:       uuid.New(),
		Handle:   "ergonomic-cotton-hoodie",
		Title:    "Ergonomic Cotton Hoodie",
		Status:   domain.ProductStatusActive,
		Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
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
	p := domain.Product{
		ID:       uuid.New(),
		Handle:   "ergonomic-cotton-hoodie",
		Title:    "Ergonomic Cotton Hoodie",
		Status:   domain.ProductStatusActive,
		Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
		Variants: []domain.Variant{
			{ID: uuid.New(), Price: decimal.NewFromFloat(59.99), Stock: 25, Options: []byte(`[]`)},
		},
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
	p := domain.Product{
		ID:       uuid.New(),
		Handle:   "ergonomic-cotton-hoodie",
		Title:    "Ergonomic Cotton Hoodie",
		Category: domain.ProductCategory{Slug: "apparel", Name: "Apparel"},
		Prices:   domain.ProductPrices{StartPrice: decimal.NewFromFloat(29.99), MaxPrice: decimal.NewFromFloat(59.99)},
	}

	raw, err := json.Marshal(p)
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

func (f *fakeQueryProductUseCase) GetByID(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	return domain.Product{}, nil
}

func (f *fakeQueryProductUseCase) GetByHandle(ctx context.Context, handle string) (domain.Product, error) {
	return domain.Product{}, nil
}

func TestProductHandler_Fetch_ParsesFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	queryUC := &fakeQueryProductUseCase{
		findAllResult: domain.PaginatedProducts{Data: []domain.Product{}},
	}
	h := NewProductHandler(nil, queryUC)

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
	h := NewProductHandler(nil, queryUC)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/products?status=bogus", nil)

	h.Fetch(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
