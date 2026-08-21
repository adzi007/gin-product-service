package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestToProductDetailData_CategoryAndStock(t *testing.T) {
	p := domain.Product{
		ID:       uuid.New(),
		Handle:   "ergonomic-cotton-hoodie",
		Title:    "Ergonomic Cotton Hoodie",
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
	if len(data.Variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(data.Variants))
	}
	// if !data.Variants[0].Stock.Equal(decimal.NewFromInt(25)) {
	// 	t.Errorf("expected variant stock 25, got %s", data.Variants[0].Stock)
	// }
	if data.Variants[0].Stock != 25 {
		t.Errorf("expected variant stock 25, got %s", data.Variants[0].Stock)
	}
}

func TestToProductDetailData_JSONOmitsCategoryID(t *testing.T) {
	p := domain.Product{
		ID:       uuid.New(),
		Handle:   "ergonomic-cotton-hoodie",
		Title:    "Ergonomic Cotton Hoodie",
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
	for _, want := range []string{`"category"`, `"slug":"apparel"`, `"name":"Apparel"`, `"stock"`, `"price"`} {
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
