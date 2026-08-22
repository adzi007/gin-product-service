package dto

import (
	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// CreateProductInput is the request body for creating a product.
type CreateProductInput struct {
	Handle      string                `json:"handle" binding:"required" validate:"required"`
	Title       string                `json:"title" binding:"required" validate:"required"`
	Description *string               `json:"description"`
	Vendor      *string               `json:"vendor"`
	CategoryID  int                   `json:"categoryId" binding:"required" validate:"required,gt=0"`
	Status      *domain.ProductStatus `json:"status" binding:"omitempty" validate:"omitempty,oneof=draft active archived"`
	Options     []ProductOptionInput  `json:"options" validate:"dive"`
	Variants    []VariantInput        `json:"variants" binding:"required" validate:"required,min=1,dive"`
	Gallery     []GalleryMediaInput   `json:"gallery" validate:"dive"`
}

func (r CreateProductInput) ToDomain() domain.CreateProductInput {
	options := make([]domain.ProductOptionInput, 0, len(r.Options))
	for _, o := range r.Options {
		options = append(options, o.ToDomain())
	}
	variants := make([]domain.VariantInput, 0, len(r.Variants))
	for _, v := range r.Variants {
		variants = append(variants, v.ToDomain())
	}
	gallery := make([]domain.GalleryMediaInput, 0, len(r.Gallery))
	for _, g := range r.Gallery {
		gallery = append(gallery, g.ToDomain())
	}
	return domain.CreateProductInput{
		Handle:      r.Handle,
		Title:       r.Title,
		Description: r.Description,
		Vendor:      r.Vendor,
		CategoryID:  r.CategoryID,
		Status:      r.Status,
		Options:     options,
		Variants:    variants,
		Gallery:     gallery,
	}
}

type ProductOptionInput struct {
	Name   string   `json:"name" binding:"required" validate:"required"`
	Values []string `json:"values" binding:"required" validate:"required,min=1"`
}

func (r ProductOptionInput) ToDomain() domain.ProductOptionInput {
	return domain.ProductOptionInput{
		Name:   r.Name,
		Values: r.Values,
	}
}

type GalleryMediaInput struct {
	ID       string  `json:"id" validate:"required"`
	Type     string  `json:"type"`
	URL      string  `json:"url" validate:"required"`
	AltText  *string `json:"altText"`
	Position int     `json:"position"`
}

func (r GalleryMediaInput) ToDomain() domain.GalleryMediaInput {
	return domain.GalleryMediaInput{
		ID:       r.ID,
		Type:     r.Type,
		URL:      r.URL,
		AltText:  r.AltText,
		Position: r.Position,
	}
}

// UpdateProductInput is the request body for PATCH /products/:id. All fields
// are optional pointers: nil means "leave this column unchanged". Only product
// header fields are updated; nested options/variants/media are never touched.
type UpdateProductInput struct {
	Title       *string               `json:"title" validate:"omitempty"`
	Description *string               `json:"description"`
	Vendor      *string               `json:"vendor"`
	Handle      *string               `json:"handle" validate:"omitempty"`
	CategoryID  *int                  `json:"categoryId" validate:"omitempty,gt=0"`
	Status      *domain.ProductStatus `json:"status" validate:"omitempty,oneof=draft active archived"`
}

func (r UpdateProductInput) ToDomain() domain.UpdateProductInput {
	return domain.UpdateProductInput{
		Title:       r.Title,
		Description: r.Description,
		Vendor:      r.Vendor,
		Handle:      r.Handle,
		CategoryID:  r.CategoryID,
		Status:      r.Status,
	}
}

// CreateOptionInput is the request body for POST /products/:id/options.
type CreateOptionInput struct {
	Name   string   `json:"name" binding:"required" validate:"required"`
	Values []string `json:"values" binding:"required" validate:"required,min=1"`
}

func (r CreateOptionInput) ToDomain() domain.CreateOptionInput {
	return domain.CreateOptionInput{
		Name:   r.Name,
		Values: r.Values,
	}
}

// UpdateOptionInput is the request body for PATCH /products/:id/options/:option_id.
type UpdateOptionInput struct {
	Name string `json:"name" binding:"required" validate:"required"`
}

func (r UpdateOptionInput) ToDomain() domain.UpdateOptionInput {
	return domain.UpdateOptionInput{
		Name: r.Name,
	}
}

// ReorderOptionsInput is the request body for PATCH /products/:id/options/reorder.
type ReorderOptionsInput struct {
	Positions []PositionUpdate `json:"positions" binding:"required" validate:"required,min=1,dive"`
}

func (r ReorderOptionsInput) ToDomain() domain.ReorderOptionsInput {
	return domain.ReorderOptionsInput{
		Positions: positionsToDomain(r.Positions),
	}
}

// PositionUpdate is shared by any reorder endpoint (options, variants and media).
type PositionUpdate struct {
	ID       uuid.UUID `json:"id" binding:"required" validate:"required"`
	Position int       `json:"position" validate:"gte=0"`
}

func positionsToDomain(positions []PositionUpdate) []domain.PositionUpdate {
	out := make([]domain.PositionUpdate, 0, len(positions))
	for _, p := range positions {
		out = append(out, domain.PositionUpdate{
			ID:       p.ID,
			Position: p.Position,
		})
	}
	return out
}

// CreateOptionValueInput is the request body for POST /products/:id/options/:option_id/values.
type CreateOptionValueInput struct {
	Value    string `json:"value" binding:"required" validate:"required"`
	Position int    `json:"position" validate:"gte=0"`
}

func (r CreateOptionValueInput) ToDomain() domain.CreateOptionValueInput {
	return domain.CreateOptionValueInput{
		Value:    r.Value,
		Position: r.Position,
	}
}

// UpdateOptionValueInput is the request body for PATCH
// /products/:id/options/:option_id/values/:value_id. Nil fields are left unchanged.
type UpdateOptionValueInput struct {
	Value    *string `json:"value"`
	Position *int    `json:"position" validate:"omitempty,gte=0"`
}

func (r UpdateOptionValueInput) ToDomain() domain.UpdateOptionValueInput {
	return domain.UpdateOptionValueInput{
		Value:    r.Value,
		Position: r.Position,
	}
}

// CreateMediaInput is a single media item for POST /products/:id/media.
type CreateMediaInput struct {
	Type     string  `json:"type" binding:"required" validate:"required,oneof=image video"`
	URL      string  `json:"url" binding:"required" validate:"required"`
	AltText  *string `json:"altText"`
	Position int     `json:"position" validate:"gte=0"`
}

func (r CreateMediaInput) ToDomain() domain.CreateMediaInput {
	return domain.CreateMediaInput{
		Type:     r.Type,
		URL:      r.URL,
		AltText:  r.AltText,
		Position: r.Position,
	}
}

// BulkCreateMediaInput is the request body for POST /products/:id/media.
type BulkCreateMediaInput struct {
	Media []CreateMediaInput `json:"media" binding:"required" validate:"required,min=1,dive"`
}

func (r BulkCreateMediaInput) ToDomain() domain.BulkCreateMediaInput {
	media := make([]domain.CreateMediaInput, 0, len(r.Media))
	for _, m := range r.Media {
		media = append(media, m.ToDomain())
	}
	return domain.BulkCreateMediaInput{Media: media}
}

// UpdateMediaInput is the request body for PATCH /products/:id/media/:media_id.
// Only alt_text is currently updatable; nil leaves it unchanged.
type UpdateMediaInput struct {
	AltText *string `json:"altText"`
}

func (r UpdateMediaInput) ToDomain() domain.UpdateMediaInput {
	return domain.UpdateMediaInput{
		AltText: r.AltText,
	}
}

// AttachVariantMediaInput is the request body for POST /variants/:id/media.
// It references an existing product_media row to link to the variant. The
// position is auto-assigned by the repository.
type AttachVariantMediaInput struct {
	MediaID uuid.UUID `json:"media_id" binding:"required" validate:"required"`
}

func (r AttachVariantMediaInput) ToDomain() domain.AttachVariantMediaInput {
	return domain.AttachVariantMediaInput{
		MediaID: r.MediaID,
	}
}

// ReorderMediaInput is the request body for PATCH /products/:id/media/reorder
// and PATCH /variants/:id/media/reorder.
type ReorderMediaInput struct {
	Positions []PositionUpdate `json:"positions" binding:"required" validate:"required,min=1,dive"`
}

// ToDomainPositions converts the request body into the positions slice expected
// by the media use case. There is no domain equivalent of ReorderMediaInput.
func (r ReorderMediaInput) ToDomainPositions() []domain.PositionUpdate {
	return positionsToDomain(r.Positions)
}
