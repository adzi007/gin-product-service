package domain

import (
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
