package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

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
	// Position is the display order of this variant within its product.
	Position int `json:"position" db:"position"`
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

// VariantUseCase is the application-layer contract for managing a product's
// variants (standalone CRUD, lifecycle and position reordering).
type VariantUseCase interface {
	Create(ctx context.Context, productID uuid.UUID, input CreateVariantInput) (Variant, error)
	BulkCreate(ctx context.Context, productID uuid.UUID, input BulkCreateVariantsInput) ([]Variant, error)
	Update(ctx context.Context, variantID uuid.UUID, input UpdateVariantInput) (Variant, error)
	BulkUpdate(ctx context.Context, productID uuid.UUID, input BulkUpdateVariantsInput) ([]Variant, error)
	Delete(ctx context.Context, variantID uuid.UUID) error
	BulkDelete(ctx context.Context, productID uuid.UUID, input BulkDeleteVariantsInput) error
	Restore(ctx context.Context, variantID uuid.UUID) (Variant, error)
	Reorder(ctx context.Context, productID uuid.UUID, positions []PositionUpdate) error
}

type VariantInput struct {
	Title          *string             `json:"title"`
	SKU            *string             `json:"sku"`
	Barcode        *string             `json:"barcode"`
	Price          *decimal.Decimal    `json:"price"`
	Weight         *decimal.Decimal    `json:"weight"`
	Options        map[string]string   `json:"options"`
	Media          []VariantMediaInput `json:"media"`
	TrackInventory *bool               `json:"track_inventory"`
	Stock          int                 `json:"stock"`
}

type VariantMediaInput struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
}

// CreateVariantInput is the request body for POST /products/:id/variants.
// It mirrors the persistable subset of VariantInput; the product id comes from
// the URL param. Media entries may reference existing product_media rows by id
// or describe brand-new media (type + url).
type CreateVariantInput struct {
	Title          *string                 `json:"title"`
	SKU            *string                 `json:"sku"`
	Barcode        *string                 `json:"barcode"`
	Price          *decimal.Decimal        `json:"price"`
	Weight         *decimal.Decimal        `json:"weight"`
	Options        map[string]string       `json:"options"`
	Media          []VariantMediaItemInput `json:"media"`
	TrackInventory *bool                   `json:"track_inventory"`
	Stock          int                     `json:"stock"`
}

// VariantMediaItemInput is one entry in a variant's "media" array. If ID is
// set, it references an existing product_media row (ownership is verified
// against the variant's product). If ID is nil, Type/URL describe brand-new
// media that must be inserted into product_media first.
type VariantMediaItemInput struct {
	ID       *uuid.UUID `json:"id"`
	Type     string     `json:"type"`
	URL      string     `json:"url"`
	AltText  *string    `json:"altText"`
	Position int        `json:"position"`
}

// BulkCreateVariantsInput is the request body for POST /products/:id/variants/bulk.
type BulkCreateVariantsInput struct {
	Variants []CreateVariantInput `json:"variants"`
}

// UpdateVariantInput is the request body for PATCH /variants/:id. All fields
// are optional pointers: nil means "leave this column unchanged". Stock is
// optional (nil = don't touch stock) and media is append-only.
type UpdateVariantInput struct {
	SKU     *string                 `json:"sku"`
	Barcode *string                 `json:"barcode"`
	Title   *string                 `json:"title"`
	Price   *decimal.Decimal        `json:"price"`
	Weight  *decimal.Decimal        `json:"weight"`
	Stock   *int                    `json:"stock"`
	Media   []VariantMediaItemInput `json:"media"`
}

// VariantUpdateItem is a single variant update within a bulk update request.
type VariantUpdateItem struct {
	ID     uuid.UUID          `json:"id"`
	Fields UpdateVariantInput `json:"fields"`
}

// BulkUpdateVariantsInput is the request body for PATCH /products/:id/variants/bulk.
type BulkUpdateVariantsInput struct {
	Updates []VariantUpdateItem `json:"updates"`
}

// BulkDeleteVariantsInput is the request body for POST /products/:id/variants/bulk-delete.
type BulkDeleteVariantsInput struct {
	IDs []uuid.UUID `json:"ids"`
}
