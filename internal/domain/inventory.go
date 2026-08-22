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
	ID             uuid.UUID  `json:"id" db:"id"`
	VariantID      *uuid.UUID `json:"variant_id,omitempty" db:"variant_id"`
	Description    *string    `json:"description,omitempty" db:"description"`
	TrackInventory bool       `json:"track_inventory" db:"track_inventory"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

type InventoryLevel struct {
	ID              uuid.UUID `json:"id" db:"id"`
	InventoryItemID uuid.UUID `json:"inventory_item_id" db:"inventory_item_id"`
	LocationID      uuid.UUID `json:"location_id" db:"location_id"`
	// AvailableQty    decimal.Decimal `json:"available_qty" db:"available_qty"`
	// ReservedQty     decimal.Decimal `json:"reserved_qty" db:"reserved_qty"`
	AvailableQty int       `json:"available_qty" db:"available_qty"`
	ReservedQty  int       `json:"reserved_qty" db:"reserved_qty"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

type StockMove struct {
	ID              uuid.UUID     `json:"id" db:"id"`
	InventoryItemID uuid.UUID     `json:"inventory_item_id" db:"inventory_item_id"`
	FromLocationID  *uuid.UUID    `json:"from_location_id,omitempty" db:"from_location_id"`
	ToLocationID    *uuid.UUID    `json:"to_location_id,omitempty" db:"to_location_id"`
	MoveType        StockMoveType `json:"move_type" db:"move_type"`
	// Quantity        decimal.Decimal `json:"quantity" db:"quantity"`
	Quantity  int        `json:"quantity" db:"quantity"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	Reason    *string    `json:"reason,omitempty" db:"reason"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}
