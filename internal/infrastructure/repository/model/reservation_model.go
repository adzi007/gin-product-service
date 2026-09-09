package model

import (
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// CheckoutReservationRequest is the durable idempotency claim for one checkout
// order.
type CheckoutReservationRequest struct {
	OrderID            uuid.UUID `db:"order_id"`
	RequestFingerprint []byte    `db:"request_fingerprint"`
	CreatedAt          time.Time `db:"created_at"`
}

// Reservation is the reservation row shape. VariantID is only populated when
// the query joins inventory_items to expose the public variant identifier.
type Reservation struct {
	ID              uuid.UUID  `db:"id"`
	InventoryItemID uuid.UUID  `db:"inventory_item_id"`
	LocationID      uuid.UUID  `db:"location_id"`
	OrderID         uuid.UUID  `db:"order_id"`
	Quantity        int        `db:"quantity"`
	ReservedAt      time.Time  `db:"reserved_at"`
	ExpiresAt       time.Time  `db:"expires_at"`
	Status          string     `db:"status"`
	ReleasedAt      *time.Time `db:"released_at"`
	VariantID       *uuid.UUID `db:"variant_id"`
}

// ToDomain converts the reservation row to the domain reservation entity.
func (m Reservation) ToDomain() domain.Reservation {
	return domain.Reservation{
		ID:              m.ID,
		InventoryItemID: m.InventoryItemID,
		LocationID:      m.LocationID,
		OrderID:         m.OrderID,
		Quantity:        domain.Quantity(m.Quantity),
		ReservedAt:      m.ReservedAt,
		ExpiresAt:       m.ExpiresAt,
		Status:          domain.ReservationStatus(m.Status),
		ReleasedAt:      m.ReleasedAt,
	}
}

// ToResultItem converts the reservation row to a public result item. It
// requires the joined public variant identifier.
func (m Reservation) ToResultItem() (domain.ReservationItemResult, bool) {
	if m.VariantID == nil {
		return domain.ReservationItemResult{}, false
	}
	return domain.ReservationItemResult{
		ReservationID: m.ID,
		VariantID:     *m.VariantID,
		Quantity:      domain.Quantity(m.Quantity),
		Status:        domain.ReservationStatus(m.Status),
		ExpiresAt:     m.ExpiresAt,
	}, true
}

// StockMove is the stock movement row shape including the optional
// reservation link added for checkout reservations.
type StockMove struct {
	ID              uuid.UUID  `db:"id"`
	InventoryItemID uuid.UUID  `db:"inventory_item_id"`
	FromLocationID  *uuid.UUID `db:"from_location_id"`
	ToLocationID    *uuid.UUID `db:"to_location_id"`
	MoveType        string     `db:"move_type"`
	Quantity        int        `db:"quantity"`
	CreatedBy       *uuid.UUID `db:"created_by"`
	Reason          *string    `db:"reason"`
	CreatedAt       time.Time  `db:"created_at"`
	ReservationID   *uuid.UUID `db:"reservation_id"`
}

// ToDomain converts the stock movement row to the domain entity.
func (m StockMove) ToDomain() domain.StockMove {
	return domain.StockMove{
		ID:              m.ID,
		InventoryItemID: m.InventoryItemID,
		FromLocationID:  m.FromLocationID,
		ToLocationID:    m.ToLocationID,
		MoveType:        domain.StockMoveType(m.MoveType),
		Quantity:        domain.Quantity(m.Quantity),
		CreatedBy:       m.CreatedBy,
		Reason:          m.Reason,
		CreatedAt:       m.CreatedAt,
	}
}
