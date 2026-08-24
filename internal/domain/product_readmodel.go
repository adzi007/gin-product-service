package domain

import "github.com/shopspring/decimal"

// ProductCategory is the nested category shape exposed on product responses.
// It intentionally omits id/thumbnail/description — only slug and name are
// surfaced to API consumers.
type ProductCategory struct {
	Id   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// ProductPrices holds the min/max variant prices exposed on product list
// responses. Both values are computed across a product's non-deleted variants.
type ProductPrices struct {
	StartPrice decimal.Decimal `json:"startPrice"`
	MaxPrice   decimal.Decimal `json:"maxPrice"`
}

// ProductThumbnail is the primary media image exposed on product list
// responses. It mirrors the Type/AltText fields of ProductMedia, sourced from
// the product_media row with position = 1.
type ProductThumbnail struct {
	Type    string  `json:"type"`
	URL     string  `json:"url"`
	AltText *string `json:"altText,omitempty"`
}

// ListProductParams carries filter/pagination/sort input for listing products.
// Search/CategoryID/Status are product-specific; the Page/PerPage/SortBy/SortDir
// fields mirror domain.ListCategoryParams in category.go — keep those field
// names consistent.
type ListProductParams struct {
	Search     string           // matches against title, handle and category name via ILIKE
	CategoryID int              // product-specific: 0 means "no filter"
	Status     string           // product-specific: empty means "no filter"; whitelisted: "draft", "active", "archived"
	MinPrice   *decimal.Decimal // optional: only include products with a variant price >= MinPrice (inclusive)
	MaxPrice   *decimal.Decimal // optional: only include products with a variant price <= MaxPrice (inclusive)
	Page       int
	PerPage    int
	SortBy     string // whitelisted: "title", "created_at", "category_name"
	SortDir    string // "asc" | "desc"
}

// PaginatedProducts carries the page data plus pagination metadata.
type PaginatedProducts struct {
	Data       []ProductListItem `json:"data"`
	Total      int               `json:"total"`
	Page       int               `json:"page"`
	PerPage    int               `json:"per_page"`
	TotalPages int               `json:"total_pages"`
}

// ProductListItem is the read-model shape for a single row returned by
// GET /products. It embeds Product for the fields shared with the write
// aggregate and adds projections that only exist at query time: the joined
// category reference, the computed min/max variant price range, and the
// primary gallery image.
type ProductListItem struct {
	Product
	Category  ProductCategory   `json:"category"`
	Prices    ProductPrices     `json:"prices"`
	Thumbnail *ProductThumbnail `json:"thumbnail"`
}

// ProductDetail is the read-model shape for a single product returned by
// GET /products/:id and GET /products/:handle. It embeds Product and adds
// the joined category reference consumed by the detail view.
type ProductDetail struct {
	Product
	Category ProductCategory `json:"category"`
}
