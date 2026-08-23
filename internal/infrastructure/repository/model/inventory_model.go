package model

import (
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type InventoryLevel struct {
	ID              uuid.UUID `db:"id"`
	InventoryItemID uuid.UUID `db:"inventory_item_id"`
	LocationID      uuid.UUID `db:"location_id"`
	AvailableQty    int       `db:"available_qty"`
	ReservedQty     int       `db:"reserved_qty"`
	UpdatedAt       time.Time `db:"updated_at"`
}

func (m InventoryLevel) ToDomain() domain.InventoryLevel {
	return domain.InventoryLevel{
		ID:              m.ID,
		InventoryItemID: m.InventoryItemID,
		LocationID:      m.LocationID,
		AvailableQty:    domain.Quantity(m.AvailableQty),
		ReservedQty:     domain.Quantity(m.ReservedQty),
		UpdatedAt:       m.UpdatedAt,
	}
}
