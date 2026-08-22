package dto

import (
	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

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

func (r VariantInput) ToDomain() domain.VariantInput {
	media := make([]domain.VariantMediaInput, 0, len(r.Media))
	for _, m := range r.Media {
		media = append(media, m.ToDomain())
	}
	return domain.VariantInput{
		Title:          r.Title,
		SKU:            r.SKU,
		Barcode:        r.Barcode,
		Price:          r.Price,
		Weight:         r.Weight,
		Options:        r.Options,
		Media:          media,
		TrackInventory: r.TrackInventory,
		Stock:          r.Stock,
	}
}

type VariantMediaInput struct {
	ID       string `json:"id" validate:"required"`
	Position int    `json:"position"`
}

func (r VariantMediaInput) ToDomain() domain.VariantMediaInput {
	return domain.VariantMediaInput{
		ID:       r.ID,
		Position: r.Position,
	}
}

// CreateVariantInput is the request body for POST /products/:id/variants.
// It mirrors the persistable subset of VariantInput; the product id comes from
// the URL param. Media entries may reference existing product_media rows by id
// or describe brand-new media (type + url).
type CreateVariantInput struct {
	Title          *string                 `json:"title"`
	SKU            *string                 `json:"sku"`
	Barcode        *string                 `json:"barcode"`
	Price          *decimal.Decimal        `json:"price" binding:"required" validate:"required"`
	Weight         *decimal.Decimal        `json:"weight" binding:"required" validate:"required"`
	Options        map[string]string       `json:"options"`
	Media          []VariantMediaItemInput `json:"media"`
	TrackInventory *bool                   `json:"track_inventory"`
	Stock          int                     `json:"stock" validate:"gte=0"`
}

func (r CreateVariantInput) ToDomain() domain.CreateVariantInput {
	media := make([]domain.VariantMediaItemInput, 0, len(r.Media))
	for _, m := range r.Media {
		media = append(media, m.ToDomain())
	}
	return domain.CreateVariantInput{
		Title:          r.Title,
		SKU:            r.SKU,
		Barcode:        r.Barcode,
		Price:          r.Price,
		Weight:         r.Weight,
		Options:        r.Options,
		Media:          media,
		TrackInventory: r.TrackInventory,
		Stock:          r.Stock,
	}
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

func (r VariantMediaItemInput) ToDomain() domain.VariantMediaItemInput {
	return domain.VariantMediaItemInput{
		ID:       r.ID,
		Type:     r.Type,
		URL:      r.URL,
		AltText:  r.AltText,
		Position: r.Position,
	}
}

// BulkCreateVariantsInput is the request body for POST /products/:id/variants/bulk.
type BulkCreateVariantsInput struct {
	Variants []CreateVariantInput `json:"variants" binding:"required" validate:"required,min=1,dive"`
}

func (r BulkCreateVariantsInput) ToDomain() domain.BulkCreateVariantsInput {
	variants := make([]domain.CreateVariantInput, 0, len(r.Variants))
	for _, v := range r.Variants {
		variants = append(variants, v.ToDomain())
	}
	return domain.BulkCreateVariantsInput{Variants: variants}
}

// UpdateVariantInput is the request body for PATCH /variants/:id. All fields
// are optional pointers: nil means "leave this column unchanged". Stock is
// optional (nil = don't touch stock) and media is append-only.
type UpdateVariantInput struct {
	SKU     *string                 `json:"sku" validate:"omitempty"`
	Barcode *string                 `json:"barcode" validate:"omitempty"`
	Title   *string                 `json:"title" validate:"omitempty"`
	Price   *decimal.Decimal        `json:"price" validate:"omitempty"`
	Weight  *decimal.Decimal        `json:"weight" validate:"omitempty"`
	Stock   *int                    `json:"stock" validate:"omitempty,gte=0"`
	Media   []VariantMediaItemInput `json:"media"`
}

func (r UpdateVariantInput) ToDomain() domain.UpdateVariantInput {
	media := make([]domain.VariantMediaItemInput, 0, len(r.Media))
	for _, m := range r.Media {
		media = append(media, m.ToDomain())
	}
	return domain.UpdateVariantInput{
		SKU:     r.SKU,
		Barcode: r.Barcode,
		Title:   r.Title,
		Price:   r.Price,
		Weight:  r.Weight,
		Stock:   r.Stock,
		Media:   media,
	}
}

// VariantUpdateItem is a single variant update within a bulk update request.
type VariantUpdateItem struct {
	ID     uuid.UUID          `json:"id" binding:"required" validate:"required"`
	Fields UpdateVariantInput `json:"fields" binding:"required"`
}

func (r VariantUpdateItem) ToDomain() domain.VariantUpdateItem {
	return domain.VariantUpdateItem{
		ID:     r.ID,
		Fields: r.Fields.ToDomain(),
	}
}

// BulkUpdateVariantsInput is the request body for PATCH /products/:id/variants/bulk.
type BulkUpdateVariantsInput struct {
	Updates []VariantUpdateItem `json:"updates" binding:"required" validate:"required,min=1,dive"`
}

func (r BulkUpdateVariantsInput) ToDomain() domain.BulkUpdateVariantsInput {
	updates := make([]domain.VariantUpdateItem, 0, len(r.Updates))
	for _, u := range r.Updates {
		updates = append(updates, u.ToDomain())
	}
	return domain.BulkUpdateVariantsInput{Updates: updates}
}

// BulkDeleteVariantsInput is the request body for POST /products/:id/variants/bulk-delete.
type BulkDeleteVariantsInput struct {
	IDs []uuid.UUID `json:"ids" binding:"required" validate:"required,min=1,dive"`
}

func (r BulkDeleteVariantsInput) ToDomain() domain.BulkDeleteVariantsInput {
	return domain.BulkDeleteVariantsInput{
		IDs: r.IDs,
	}
}

// ReorderVariantsInput is the request body for PATCH /products/:id/variants/reorder.
type ReorderVariantsInput struct {
	Positions []PositionUpdate `json:"positions" binding:"required" validate:"required,min=1,dive"`
}

// ToDomainPositions converts the request body into the positions slice expected
// by the variant use case. There is no domain equivalent of ReorderVariantsInput.
func (r ReorderVariantsInput) ToDomainPositions() []domain.PositionUpdate {
	return positionsToDomain(r.Positions)
}
