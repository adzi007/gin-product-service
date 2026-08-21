package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StockMoveType enumerates the allowed stock movement kinds.
type StockMoveType string

const (
	StockMoveIn        StockMoveType = "IN"
	StockMoveOut       StockMoveType = "OUT"
	StockMoveTransfer  StockMoveType = "TRANSFER"
	StockMoveAdjust    StockMoveType = "ADJUST"
	StockMoveReserve   StockMoveType = "RESERVE"
	StockMoveUnreserve StockMoveType = "UNRESERVE"
)

// ProductStatus enumerates the allowed product lifecycle states.
type ProductStatus string

const (
	ProductStatusDraft    ProductStatus = "draft"
	ProductStatusActive   ProductStatus = "active"
	ProductStatusArchived ProductStatus = "archived"
)

// Product is the catalog master record.
type Product struct {
	ID          uuid.UUID         `json:"id" db:"id"`
	Handle      string            `json:"handle" db:"handle"`
	Title       string            `json:"title" db:"title"`
	Status      ProductStatus     `json:"status" db:"status"`
	Thumbnail   *ProductThumbnail `json:"thumbnail" db:"-"`
	Description *string           `json:"description,omitempty" db:"description"`
	Vendor      *string           `json:"vendor,omitempty" db:"vendor"`
	CategoryID  int               `json:"-" db:"category_id"`
	Category    ProductCategory   `json:"category" db:"-"`
	Prices      ProductPrices     `json:"prices" db:"-"`
	Options     []ProductOption   `json:"options,omitempty" db:"-"`
	Variants    []Variant         `json:"variants,omitempty" db:"-"`
	Media       []ProductMedia    `json:"media,omitempty" db:"-"`
	CreatedAt   time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt   *time.Time        `json:"updated_at,omitempty" db:"updated_at"`
}

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

type ProductOption struct {
	ID        uuid.UUID            `json:"id" db:"id"`
	ProductID uuid.UUID            `json:"product_id" db:"product_id"`
	Name      string               `json:"name" db:"name"`
	Position  int                  `json:"position" db:"position"`
	Values    []ProductOptionValue `json:"values,omitempty" db:"-"`
}

type ProductOptionValue struct {
	ID       uuid.UUID `json:"id" db:"id"`
	OptionID uuid.UUID `json:"option_id" db:"option_id"`
	Value    string    `json:"value" db:"value"`
	Position int       `json:"position" db:"position"`
}

// VariantOption is the typed representation of a single entry in the
// variants.options JSONB column: [{"option":"Color","value":"Black"}].
type VariantOption struct {
	Option string `json:"option"`
	Value  string `json:"value"`
}

type Variant struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	ProductID uuid.UUID       `json:"product_id" db:"product_id"`
	SKU       *string         `json:"sku,omitempty" db:"sku"`
	Barcode   *string         `json:"barcode,omitempty" db:"barcode"`
	Title     *string         `json:"title,omitempty" db:"title"`
	Price     decimal.Decimal `json:"price" db:"price"`
	Weight    decimal.Decimal `json:"weight" db:"weight"`
	// Stock is the total available quantity across all inventory levels for
	// this variant. It is computed (not stored) and only populated on the
	// single-product detail path.
	// Stock decimal.Decimal `json:"stock" db:"stock"`
	Stock int `json:"stock" db:"stock"`
	// Options stores the raw JSONB: [{"option":"Color","value":"Black"}].
	Options   []byte         `json:"-" db:"options"`
	IsDeleted bool           `json:"is_deleted" db:"is_deleted"`
	Media     []VariantMedia `json:"media,omitempty" db:"-"`
	CreatedAt time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt *time.Time     `json:"updated_at,omitempty" db:"updated_at"`
}

// VariantMedia links a variant to a product_media row.
type VariantMedia struct {
	VariantID uuid.UUID `json:"-" db:"variant_id"`
	MediaID   uuid.UUID `json:"id" db:"media_id"`
	Position  int       `json:"position" db:"position"`
}

type ProductMedia struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	ProductID uuid.UUID  `json:"product_id" db:"product_id"`
	Type      string     `json:"type" db:"type"` // "image", "video"
	URL       string     `json:"url" db:"url"`
	AltText   *string    `json:"alt_text,omitempty" db:"alt_text"`
	Position  int        `json:"position" db:"position"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty" db:"updated_at"`
}

type InventoryItem struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	VariantID      *uuid.UUID `json:"variant_id,omitempty" db:"variant_id"`
	Description    *string    `json:"description,omitempty" db:"description"`
	TrackInventory bool       `json:"track_inventory" db:"track_inventory"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

type InventoryLevel struct {
	ID              uuid.UUID `json:"id" db:"id"`
	InventoryItemID uuid.UUID `json:"inventory_item_id" db:"inventory_item_id"`
	LocationID      uuid.UUID `json:"location_id" db:"location_id"`
	// AvailableQty    decimal.Decimal `json:"available_qty" db:"available_qty"`
	// ReservedQty     decimal.Decimal `json:"reserved_qty" db:"reserved_qty"`
	AvailableQty int       `json:"available_qty" db:"available_qty"`
	ReservedQty  int       `json:"reserved_qty" db:"reserved_qty"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

type StockMove struct {
	ID              uuid.UUID     `json:"id" db:"id"`
	InventoryItemID uuid.UUID     `json:"inventory_item_id" db:"inventory_item_id"`
	FromLocationID  *uuid.UUID    `json:"from_location_id,omitempty" db:"from_location_id"`
	ToLocationID    *uuid.UUID    `json:"to_location_id,omitempty" db:"to_location_id"`
	MoveType        StockMoveType `json:"move_type" db:"move_type"`
	// Quantity        decimal.Decimal `json:"quantity" db:"quantity"`
	Quantity  int        `json:"quantity" db:"quantity"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	Reason    *string    `json:"reason,omitempty" db:"reason"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// Domain errors surfaced by the product module.
var (
	// ErrProductInvalidInput is returned when a create request fails basic
	// validation at the use case layer.
	ErrProductInvalidInput = errors.New("invalid product input")
	// ErrSKUAlreadyExists is returned when a variant SKU is duplicated within
	// the request or violates the database unique constraint.
	ErrSKUAlreadyExists = errors.New("SKU already exists")
	// ErrInvalidMediaAssignment is returned when a variant media reference does
	// not resolve to any gallery entry in the request.
	ErrInvalidMediaAssignment = errors.New("invalid media assignment")
	// ErrInvalidOption is returned when a variant option key/value does not
	// match the declared product options.
	ErrInvalidOption = errors.New("invalid variant option")
	// ErrDefaultLocationNotFound is returned when no location is flagged as the
	// default location.
	ErrDefaultLocationNotFound = errors.New("default location not configured")
	// ErrProductNotFound is returned when no product matches the given id/handle.
	ErrProductNotFound = errors.New("product not found")
	// ErrProductInvalidStatus is returned when an unrecognized product status
	// string is passed to the use case or handler.
	ErrProductInvalidStatus = errors.New("invalid product status")
	// ErrOptionNotFound is returned when no product option matches the given id.
	ErrOptionNotFound = errors.New("option not found")
	// ErrOptionValueNotFound is returned when no product option value matches
	// the given id.
	ErrOptionValueNotFound = errors.New("option value not found")
	// ErrOptionAlreadyExists is returned when an option name duplicates another
	// option name on the same product.
	ErrOptionAlreadyExists = errors.New("option already exists")
	// ErrProductHandleAlreadyExists is returned when a handle duplicates another
	// product's handle (store-wide uniqueness).
	ErrProductHandleAlreadyExists = errors.New("handle already exists")
	// ErrProductHasHistory is returned when a purge is blocked because the
	// product's variants have stock movement history.
	ErrProductHasHistory = errors.New("product has stock history")
)

// CreateProductParams carries everything the repository needs to persist a new
// product (and its related rows) atomically in one transaction. All UUIDs are
// generated in the use case layer before calling the repository.
type CreateProductParams struct {
	Product         Product
	InventoryItems  []InventoryItem
	StockMoves      []StockMove
	InventoryLevels []InventoryLevel
}

// InsertProductUseCase is the application-layer contract for creating a product.
type InsertProductUseCase interface {
	Create(ctx context.Context, input CreateProductInput) (Product, error)
}

// ListProductParams carries filter/pagination/sort input for listing products.
// Search/CategoryID/Status are product-specific; the Page/PerPage/SortBy/SortDir
// fields mirror domain.ListCategoryParams in category.go — keep those field
// names consistent.
type ListProductParams struct {
	Search     string // matches against title, handle and category name via ILIKE
	CategoryID int    // product-specific: 0 means "no filter"
	Status     string // product-specific: empty means "no filter"; whitelisted: "draft", "active", "archived"
	Page       int
	PerPage    int
	SortBy     string // whitelisted: "title", "created_at", "category_name"
	SortDir    string // "asc" | "desc"
}

// PaginatedProducts carries the page data plus pagination metadata.
type PaginatedProducts struct {
	Data       []Product `json:"data"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	PerPage    int       `json:"per_page"`
	TotalPages int       `json:"total_pages"`
}

// QueryProductUseCase is the application-layer contract for reading products.
type QueryProductUseCase interface {
	FindAll(ctx context.Context, params ListProductParams) (PaginatedProducts, error)
	GetByID(ctx context.Context, id uuid.UUID) (Product, error)
	GetByHandle(ctx context.Context, handle string) (Product, error)
}

// UpdateProductUseCase is the application-layer contract for updating a product
// header and managing its lifecycle (archive/restore).
type UpdateProductUseCase interface {
	Update(ctx context.Context, id uuid.UUID, input UpdateProductInput) (Product, error)
	Archive(ctx context.Context, id uuid.UUID) error
	Restore(ctx context.Context, id uuid.UUID) error
}

// DeleteProductUseCase is the application-layer contract for hard-deleting
// (purging) a product.
type DeleteProductUseCase interface {
	Purge(ctx context.Context, id uuid.UUID) error
}

// OptionUseCase is the application-layer contract for managing a product's
// options and their option values.
type OptionUseCase interface {
	Create(ctx context.Context, productID uuid.UUID, input CreateOptionInput) (ProductOption, error)
	Rename(ctx context.Context, productID, optionID uuid.UUID, input UpdateOptionInput) (ProductOption, error)
	Delete(ctx context.Context, productID, optionID uuid.UUID) error
	Reorder(ctx context.Context, productID uuid.UUID, input ReorderOptionsInput) error
	AddValue(ctx context.Context, productID, optionID uuid.UUID, input CreateOptionValueInput) (ProductOptionValue, error)
	UpdateValue(ctx context.Context, productID, optionID, valueID uuid.UUID, input UpdateOptionValueInput) (ProductOptionValue, error)
	DeleteValue(ctx context.Context, productID, optionID, valueID uuid.UUID) error
}

// ProductRepository is the persistence contract for the product module.
type ProductRepository interface {
	Create(ctx context.Context, params CreateProductParams) (Product, error)
	FindAll(ctx context.Context, params ListProductParams) ([]Product, int, error)
	FindByID(ctx context.Context, id uuid.UUID) (Product, error)
	FindByHandle(ctx context.Context, handle string) (Product, error)

	UpdateHeader(ctx context.Context, id uuid.UUID, input UpdateProductInput) (Product, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status ProductStatus) error
	Delete(ctx context.Context, id uuid.UUID) error

	CreateOption(ctx context.Context, option ProductOption) (ProductOption, error)
	RenameOption(ctx context.Context, productID, optionID uuid.UUID, name string) (ProductOption, error)
	DeleteOption(ctx context.Context, productID, optionID uuid.UUID) error
	ReorderOptions(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	FindOptionByID(ctx context.Context, optionID uuid.UUID) (ProductOption, error)

	CreateOptionValue(ctx context.Context, value ProductOptionValue) (ProductOptionValue, error)
	UpdateOptionValue(ctx context.Context, valueID uuid.UUID, value *string, position *int) (ProductOptionValue, error)
	DeleteOptionValue(ctx context.Context, valueID uuid.UUID) error
	FindOptionValueByID(ctx context.Context, valueID uuid.UUID) (ProductOptionValue, error)
}

// CreateProductInput is the request body for creating a product.
type CreateProductInput struct {
	Handle      string               `json:"handle" binding:"required" validate:"required"`
	Title       string               `json:"title" binding:"required" validate:"required"`
	Description *string              `json:"description"`
	Vendor      *string              `json:"vendor"`
	CategoryID  int                  `json:"categoryId" binding:"required" validate:"required,gt=0"`
	Status      *ProductStatus       `json:"status" binding:"omitempty" validate:"omitempty,oneof=draft active archived"`
	Options     []ProductOptionInput `json:"options" validate:"dive"`
	Variants    []VariantInput       `json:"variants" binding:"required" validate:"required,min=1,dive"`
	Gallery     []GalleryMediaInput  `json:"gallery" validate:"dive"`
}

type ProductOptionInput struct {
	Name   string   `json:"name" binding:"required" validate:"required"`
	Values []string `json:"values" binding:"required" validate:"required,min=1"`
}

type VariantInput struct {
	Title          *string             `json:"title"`
	SKU            *string             `json:"sku"`
	Barcode        *string             `json:"barcode"`
	Price          *decimal.Decimal    `json:"price" binding:"required" validate:"required"`
	Weight         *decimal.Decimal    `json:"weight" binding:"required" validate:"required"`
	Options        map[string]string   `json:"options"`
	Media          []VariantMediaInput `json:"media"`
	TrackInventory *bool               `json:"track_inventory"`
	Stock          int                 `json:"stock" validate:"gte=0"`
}

type VariantMediaInput struct {
	ID       string `json:"id" validate:"required"`
	Position int    `json:"position"`
}

type GalleryMediaInput struct {
	ID       string  `json:"id" validate:"required"`
	Type     string  `json:"type"`
	URL      string  `json:"url" validate:"required"`
	AltText  *string `json:"altText"`
	Position int     `json:"position"`
}

// UpdateProductInput is the request body for PATCH /products/:id. All fields
// are optional pointers: nil means "leave this column unchanged". Only product
// header fields are updated; nested options/variants/media are never touched.
type UpdateProductInput struct {
	Title       *string        `json:"title" validate:"omitempty"`
	Description *string        `json:"description"`
	Vendor      *string        `json:"vendor"`
	Handle      *string        `json:"handle" validate:"omitempty"`
	CategoryID  *int           `json:"categoryId" validate:"omitempty,gt=0"`
	Status      *ProductStatus `json:"status" validate:"omitempty,oneof=draft active archived"`
}

// CreateOptionInput is the request body for POST /products/:id/options.
type CreateOptionInput struct {
	Name   string   `json:"name" binding:"required" validate:"required"`
	Values []string `json:"values" binding:"required" validate:"required,min=1"`
}

// UpdateOptionInput is the request body for PATCH /products/:id/options/:option_id.
type UpdateOptionInput struct {
	Name string `json:"name" binding:"required" validate:"required"`
}

// ReorderOptionsInput is the request body for PATCH /products/:id/options/reorder.
type ReorderOptionsInput struct {
	Positions []PositionUpdate `json:"positions" binding:"required" validate:"required,min=1,dive"`
}

// PositionUpdate is shared by any reorder endpoint (options today, variants later).
type PositionUpdate struct {
	ID       uuid.UUID `json:"id" binding:"required" validate:"required"`
	Position int       `json:"position" validate:"gte=0"`
}

// CreateOptionValueInput is the request body for POST /products/:id/options/:option_id/values.
type CreateOptionValueInput struct {
	Value    string `json:"value" binding:"required" validate:"required"`
	Position int    `json:"position" validate:"gte=0"`
}

// UpdateOptionValueInput is the request body for PATCH
// /products/:id/options/:option_id/values/:value_id. Nil fields are left unchanged.
type UpdateOptionValueInput struct {
	Value    *string `json:"value"`
	Position *int    `json:"position" validate:"omitempty,gte=0"`
}
