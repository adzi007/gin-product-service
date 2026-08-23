package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProductStatus enumerates the allowed product lifecycle states.
type ProductStatus string

const (
	ProductStatusDraft    ProductStatus = "draft"
	ProductStatusActive   ProductStatus = "active"
	ProductStatusArchived ProductStatus = "archived"
)

// CanTransitionTo reports whether transitioning from s to next is allowed.
// Allowed transitions: draft -> active, draft -> archived, active -> archived,
// archived -> active (restore). All other transitions, including transitioning
// to the same status, are rejected.
func (s ProductStatus) CanTransitionTo(next ProductStatus) bool {
	switch s {
	case ProductStatusDraft:
		return next == ProductStatusActive || next == ProductStatusArchived
	case ProductStatusActive:
		return next == ProductStatusArchived
	case ProductStatusArchived:
		return next == ProductStatusActive
	default:
		return false
	}
}

// Product is the catalog master record.
type Product struct {
	ID          uuid.UUID       `json:"id"`
	Handle      string          `json:"handle"`
	Title       string          `json:"title"`
	Status      ProductStatus   `json:"status"`
	Description *string         `json:"description,omitempty"`
	Vendor      *string         `json:"vendor,omitempty"`
	CategoryID  int             `json:"-"`
	Options     []ProductOption `json:"options,omitempty"`
	Variants    []Variant       `json:"variants,omitempty"`
	Media       []ProductMedia  `json:"media,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
}

// Archive transitions the product to the archived status. It mutates p in
// place and returns ErrProductInvalidStatusTransition if the current status
// cannot transition to archived.
func (p *Product) Archive() error {
	if !p.Status.CanTransitionTo(ProductStatusArchived) {
		return ErrProductInvalidStatusTransition
	}
	p.Status = ProductStatusArchived
	return nil
}

// Restore transitions an archived product back to active. It mutates p in
// place and returns ErrProductInvalidStatusTransition if the current status
// cannot transition to active.
func (p *Product) Restore() error {
	if !p.Status.CanTransitionTo(ProductStatusActive) {
		return ErrProductInvalidStatusTransition
	}
	p.Status = ProductStatusActive
	return nil
}

// NewProduct constructs a new draft product with a generated ID, validating the
// minimal set of fields required for any product to exist. Callers set
// Description/Vendor/Options/Media directly on the returned value, and use
// AddVariant to attach variants.
func NewProduct(handle, title string, categoryID int) (*Product, error) {
	if strings.TrimSpace(handle) == "" || strings.TrimSpace(title) == "" {
		return nil, ErrProductInvalidInput
	}
	if categoryID <= 0 {
		return nil, ErrProductInvalidInput
	}
	return &Product{
		ID:         uuid.Must(uuid.NewV7()),
		Handle:     handle,
		Title:      title,
		Status:     ProductStatusDraft,
		CategoryID: categoryID,
	}, nil
}

// AddVariant appends v to the product's variant list, enforcing that its SKU
// (if set) does not duplicate an existing variant's SKU on this product. Empty
// SKUs are not checked for uniqueness — multiple variants may have no SKU.
func (p *Product) AddVariant(v Variant) error {
	if v.SKU != nil && strings.TrimSpace(*v.SKU) != "" {
		for _, existing := range p.Variants {
			if existing.SKU != nil && *existing.SKU == *v.SKU {
				return ErrSKUAlreadyExists
			}
		}
	}
	p.Variants = append(p.Variants, v)
	return nil
}

type ProductOption struct {
	ID        uuid.UUID            `json:"id"`
	ProductID uuid.UUID            `json:"product_id"`
	Name      string               `json:"name"`
	Position  int                  `json:"position"`
	Values    []ProductOptionValue `json:"values,omitempty"`
}

type ProductOptionValue struct {
	ID       uuid.UUID `json:"id"`
	OptionID uuid.UUID `json:"option_id"`
	Value    string    `json:"value"`
	Position int       `json:"position"`
}

type ProductMedia struct {
	ID        uuid.UUID  `json:"id"`
	ProductID uuid.UUID  `json:"product_id"`
	Type      string     `json:"type"` // "image", "video"
	URL       string     `json:"url"`
	AltText   *string    `json:"alt_text,omitempty"`
	Position  int        `json:"position"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
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
	// ErrProductInvalidStatusTransition is returned when a status change is not
	// allowed from the product's current status (e.g. archived -> draft).
	ErrProductInvalidStatusTransition = errors.New("invalid product status transition")
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
	// ErrVariantNotFound is returned when no variant matches the given id.
	ErrVariantNotFound = errors.New("variant not found")
	// ErrMediaNotFound is returned when no product media matches the given id,
	// or when the media does not belong to the referenced product/variant.
	ErrMediaNotFound = errors.New("media not found")
	// ErrVariantMediaNotFound is returned when a variant is not linked to the
	// given media id.
	ErrVariantMediaNotFound = errors.New("variant media not found")
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

// CreateVariantsParams carries everything the repository needs to persist one
// or more new variants (and their inventory rows/media) atomically. Used by
// both the single-create and bulk-create variant endpoints.
type CreateVariantsParams struct {
	Variants        []Variant
	InventoryItems  []InventoryItem
	StockMoves      []StockMove
	InventoryLevels []InventoryLevel
	NewMedia        []ProductMedia // brand-new media to insert into product_media
	VariantMedia    []VariantMedia // links to insert into variant_media (covers both new and existing media)
}

// InsertProductUseCase is the application-layer contract for creating a product.
type InsertProductUseCase interface {
	Create(ctx context.Context, input CreateProductInput) (Product, error)
}

// QueryProductUseCase is the application-layer contract for reading products.
type QueryProductUseCase interface {
	FindAll(ctx context.Context, params ListProductParams) (PaginatedProducts, error)
	GetByID(ctx context.Context, id uuid.UUID) (ProductDetail, error)
	GetByHandle(ctx context.Context, handle string) (ProductDetail, error)
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

// MediaUseCase is the application-layer contract for managing a product's
// media gallery and attaching/detaching media to variants.
type MediaUseCase interface {
	Create(ctx context.Context, productID uuid.UUID, input BulkCreateMediaInput) ([]ProductMedia, error)
	Update(ctx context.Context, productID, mediaID uuid.UUID, input UpdateMediaInput) (ProductMedia, error)
	Delete(ctx context.Context, productID, mediaID uuid.UUID) error
	Reorder(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	AttachToVariant(ctx context.Context, variantID uuid.UUID, input AttachVariantMediaInput) (VariantMedia, error)
	DetachFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) error
	ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []PositionUpdate) error
}

// ProductRepository is the persistence contract for the product module.
type ProductRepository interface {
	Create(ctx context.Context, params CreateProductParams) (Product, error)
	FindAll(ctx context.Context, params ListProductParams) ([]ProductListItem, int, error)
	FindByID(ctx context.Context, id uuid.UUID) (ProductDetail, error)
	FindByHandle(ctx context.Context, handle string) (ProductDetail, error)

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

	CreateVariant(ctx context.Context, variant Variant) (Variant, error)
	CreateVariants(ctx context.Context, variants []Variant) ([]Variant, error)
	CreateVariantsWithStock(ctx context.Context, params CreateVariantsParams) ([]Variant, error)
	AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty int) (InventoryLevel, error)
	FindVariantByID(ctx context.Context, variantID uuid.UUID) (Variant, error)
	UpdateVariant(ctx context.Context, variantID uuid.UUID, input UpdateVariantInput) (Variant, error)
	DeleteVariant(ctx context.Context, variantID uuid.UUID, hard bool) error
	BulkDeleteVariants(ctx context.Context, variantIDs []uuid.UUID, hard bool) error
	RestoreVariant(ctx context.Context, variantID uuid.UUID) (Variant, error)
	ReorderVariants(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	// VariantHasHistory reports whether a variant has any stock movement
	// history, which decides soft vs hard deletion.
	VariantHasHistory(ctx context.Context, variantID uuid.UUID) (bool, error)

	CreateProductMedia(ctx context.Context, media []ProductMedia) ([]ProductMedia, error)
	UpdateProductMedia(ctx context.Context, mediaID uuid.UUID, altText *string) (ProductMedia, error)
	DeleteProductMedia(ctx context.Context, mediaID uuid.UUID) error
	ReorderProductMedia(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
	FindMediaByID(ctx context.Context, mediaID uuid.UUID) (ProductMedia, error)

	AttachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) (VariantMedia, error)
	DetachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) error
	ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []PositionUpdate) error

	// AttachNewOrExistingVariantMedia appends media links to a variant during
	// update. It inserts any brand-new media rows (newMedia) into product_media
	// and then inserts the variant_media links; it never touches existing links.
	AttachNewOrExistingVariantMedia(ctx context.Context, newMedia []ProductMedia, links []VariantMedia) error
}

// CreateProductInput is the request body for creating a product.
type CreateProductInput struct {
	Handle      string               `json:"handle"`
	Title       string               `json:"title"`
	Description *string              `json:"description"`
	Vendor      *string              `json:"vendor"`
	CategoryID  int                  `json:"categoryId"`
	Status      *ProductStatus       `json:"status"`
	Options     []ProductOptionInput `json:"options"`
	Variants    []VariantInput       `json:"variants"`
	Gallery     []GalleryMediaInput  `json:"gallery"`
}

type ProductOptionInput struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type GalleryMediaInput struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	URL      string  `json:"url"`
	AltText  *string `json:"altText"`
	Position int     `json:"position"`
}

// UpdateProductInput is the request body for PATCH /products/:id. All fields
// are optional pointers: nil means "leave this column unchanged". Only product
// header fields are updated; nested options/variants/media are never touched.
type UpdateProductInput struct {
	Title       *string        `json:"title"`
	Description *string        `json:"description"`
	Vendor      *string        `json:"vendor"`
	Handle      *string        `json:"handle"`
	CategoryID  *int           `json:"categoryId"`
	Status      *ProductStatus `json:"status"`
}

// CreateOptionInput is the request body for POST /products/:id/options.
type CreateOptionInput struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// UpdateOptionInput is the request body for PATCH /products/:id/options/:option_id.
type UpdateOptionInput struct {
	Name string `json:"name"`
}

// ReorderOptionsInput is the request body for PATCH /products/:id/options/reorder.
type ReorderOptionsInput struct {
	Positions []PositionUpdate `json:"positions"`
}

// PositionUpdate is shared by any reorder endpoint (options, variants and media).
type PositionUpdate struct {
	ID       uuid.UUID `json:"id"`
	Position int       `json:"position"`
}

// CreateOptionValueInput is the request body for POST /products/:id/options/:option_id/values.
type CreateOptionValueInput struct {
	Value    string `json:"value"`
	Position int    `json:"position"`
}

// UpdateOptionValueInput is the request body for PATCH
// /products/:id/options/:option_id/values/:value_id. Nil fields are left unchanged.
type UpdateOptionValueInput struct {
	Value    *string `json:"value"`
	Position *int    `json:"position"`
}

// CreateMediaInput is a single media item for POST /products/:id/media.
type CreateMediaInput struct {
	Type     string  `json:"type"`
	URL      string  `json:"url"`
	AltText  *string `json:"altText"`
	Position int     `json:"position"`
}

// BulkCreateMediaInput is the request body for POST /products/:id/media.
type BulkCreateMediaInput struct {
	Media []CreateMediaInput `json:"media"`
}

// UpdateMediaInput is the request body for PATCH /products/:id/media/:media_id.
// Only alt_text is currently updatable; nil leaves it unchanged.
type UpdateMediaInput struct {
	AltText *string `json:"altText"`
}

// AttachVariantMediaInput is the request body for POST /variants/:id/media.
// It references an existing product_media row to link to the variant. The
// position is auto-assigned by the repository.
type AttachVariantMediaInput struct {
	MediaID uuid.UUID `json:"media_id"`
}
