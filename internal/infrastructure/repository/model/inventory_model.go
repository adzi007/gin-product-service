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

// Reservation mirrors the reservations table joined with inventory_items so the
// variant_id (the cross-service identifier) is available on reads. VariantID is
// not a column of reservations; it is populated from the join.
type Reservation struct {
	ID              uuid.UUID                `db:"id"`
	InventoryItemID uuid.UUID                `db:"inventory_item_id"`
	VariantID       uuid.UUID                `db:"variant_id"`
	LocationID      uuid.UUID                `db:"location_id"`
	OrderID         *uuid.UUID               `db:"order_id"`
	Quantity        int                      `db:"quantity"`
	ReservedAt      time.Time                `db:"reserved_at"`
	ExpiresAt       *time.Time               `db:"expires_at"`
	Status          domain.ReservationStatus `db:"status"`
	ReleasedAt      *time.Time               `db:"released_at"`
}

func (m Reservation) ToDomain() domain.Reservation {
	return domain.Reservation{
		ID:              m.ID,
		InventoryItemID: m.InventoryItemID,
		VariantID:       m.VariantID,
		LocationID:      m.LocationID,
		OrderID:         m.OrderID,
		Quantity:        domain.Quantity(m.Quantity),
		ReservedAt:      m.ReservedAt,
		ExpiresAt:       m.ExpiresAt,
		Status:          m.Status,
		ReleasedAt:      m.ReleasedAt,
	}
}
