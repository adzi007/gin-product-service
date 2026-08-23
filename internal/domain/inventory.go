package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
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

var (
	// ErrInvalidQuantity is returned when a negative quantity is passed to an
	// inventory-mutating method.
	ErrInvalidQuantity = errors.New("quantity must not be negative")
	// ErrInsufficientStock is returned when a reservation would exceed the
	// currently available quantity.
	ErrInsufficientStock = errors.New("insufficient available stock")
)

type InventoryItem struct {
	ID             uuid.UUID  `json:"id"`
	VariantID      *uuid.UUID `json:"variant_id,omitempty"`
	Description    *string    `json:"description,omitempty"`
	TrackInventory bool       `json:"track_inventory"`
	CreatedAt      time.Time  `json:"created_at"`
}

type InventoryLevel struct {
	ID              uuid.UUID `json:"id"`
	InventoryItemID uuid.UUID `json:"inventory_item_id"`
	LocationID      uuid.UUID `json:"location_id"`
	AvailableQty    int       `json:"available_qty"`
	ReservedQty     int       `json:"reserved_qty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Reserve moves qty units from AvailableQty to ReservedQty. It returns
// ErrInvalidQuantity if qty is negative, or ErrInsufficientStock if qty
// exceeds the currently available quantity. On success it mutates l in place.
func (l *InventoryLevel) Reserve(qty int) error {
	if qty < 0 {
		return ErrInvalidQuantity
	}
	if qty > l.AvailableQty {
		return ErrInsufficientStock
	}
	l.AvailableQty -= qty
	l.ReservedQty += qty
	return nil
}

type StockMove struct {
	ID              uuid.UUID     `json:"id"`
	InventoryItemID uuid.UUID     `json:"inventory_item_id"`
	FromLocationID  *uuid.UUID    `json:"from_location_id,omitempty"`
	ToLocationID    *uuid.UUID    `json:"to_location_id,omitempty"`
	MoveType        StockMoveType `json:"move_type"`
	Quantity        int           `json:"quantity"`
	CreatedBy       *uuid.UUID    `json:"created_by,omitempty"`
	Reason          *string       `json:"reason,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
}
