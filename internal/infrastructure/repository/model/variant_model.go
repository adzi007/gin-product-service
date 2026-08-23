package model

import (
	"time"

	"gin-product-service/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Variant struct {
	ID        uuid.UUID       `db:"id"`
	ProductID uuid.UUID       `db:"product_id"`
	SKU       *string         `db:"sku"`
	Barcode   *string         `db:"barcode"`
	Title     *string         `db:"title"`
	Price     decimal.Decimal `db:"price"`
	Weight    decimal.Decimal `db:"weight"`
	Position  int             `db:"position"`
	Stock     int             `db:"stock"`
	Options   []byte          `db:"options"`
	IsDeleted bool            `db:"is_deleted"`
	CreatedAt time.Time       `db:"created_at"`
	UpdatedAt *time.Time      `db:"updated_at"`
}

func (m Variant) ToDomain() domain.Variant {
	return domain.Variant{
		ID:        m.ID,
		ProductID: m.ProductID,
		SKU:       m.SKU,
		Barcode:   m.Barcode,
		Title:     m.Title,
		Price:     m.Price,
		Weight:    m.Weight,
		Position:  m.Position,
		Stock:     m.Stock,
		Options:   m.Options,
		IsDeleted: m.IsDeleted,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

type VariantMedia struct {
	VariantID uuid.UUID `db:"variant_id"`
	MediaID   uuid.UUID `db:"media_id"`
	Position  int       `db:"position"`
}

func (m VariantMedia) ToDomain() domain.VariantMedia {
	return domain.VariantMedia{
		VariantID: m.VariantID,
		MediaID:   m.MediaID,
		Position:  m.Position,
	}
}
