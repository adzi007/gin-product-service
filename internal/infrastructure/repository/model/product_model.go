package model

import (
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type Product struct {
	ID          uuid.UUID  `db:"id"`
	Handle      string     `db:"handle"`
	Title       string     `db:"title"`
	Status      string     `db:"status"`
	Description *string    `db:"description"`
	Vendor      *string    `db:"vendor"`
	CategoryID  int        `db:"category_id"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`
}

func (m Product) ToDomain() domain.Product {
	return domain.Product{
		ID:          m.ID,
		Handle:      m.Handle,
		Title:       m.Title,
		Status:      domain.ProductStatus(m.Status),
		Description: m.Description,
		Vendor:      m.Vendor,
		CategoryID:  m.CategoryID,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

type ProductOption struct {
	ID        uuid.UUID `db:"id"`
	ProductID uuid.UUID `db:"product_id"`
	Name      string    `db:"name"`
	Position  int       `db:"position"`
}

func (m ProductOption) ToDomain() domain.ProductOption {
	return domain.ProductOption{
		ID:        m.ID,
		ProductID: m.ProductID,
		Name:      m.Name,
		Position:  m.Position,
	}
}

type ProductOptionValue struct {
	ID       uuid.UUID `db:"id"`
	OptionID uuid.UUID `db:"option_id"`
	Value    string    `db:"value"`
	Position int       `db:"position"`
}

func (m ProductOptionValue) ToDomain() domain.ProductOptionValue {
	return domain.ProductOptionValue{
		ID:       m.ID,
		OptionID: m.OptionID,
		Value:    m.Value,
		Position: m.Position,
	}
}

type ProductMedia struct {
	ID        uuid.UUID  `db:"id"`
	ProductID uuid.UUID  `db:"product_id"`
	Type      string     `db:"type"`
	URL       string     `db:"url"`
	AltText   *string    `db:"alt_text"`
	Position  int        `db:"position"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

func (m ProductMedia) ToDomain() domain.ProductMedia {
	return domain.ProductMedia{
		ID:        m.ID,
		ProductID: m.ProductID,
		Type:      m.Type,
		URL:       m.URL,
		AltText:   m.AltText,
		Position:  m.Position,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
