package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ReservationStatus enumerates the lifecycle states of a reservation
// (spec Section 2.5).
type ReservationStatus string

const (
	ReservationActive    ReservationStatus = "ACTIVE"
	ReservationCompleted ReservationStatus = "COMPLETED"
	ReservationCancelled ReservationStatus = "CANCELLED"
	ReservationExpired   ReservationStatus = "EXPIRED"
)

// Errors surfaced by the reservation module. ErrVariantNotFound,
// ErrLocationNotFound and ErrInventoryLevelNotFound are shared with the
// stock-move module (see inventory.go).
var (
	// ErrInvalidReservationInput is returned when a reservation request fails
	// basic validation (spec Section 9: missing order_id, empty items, invalid
	// item fields, quantity <= 0).
	ErrInvalidReservationInput = errors.New("invalid reservation input")
	// ErrReservationNotFound is returned when an order has never had any
	// reservation (spec Section 14).
	ErrReservationNotFound = errors.New("reservation not found")
	// ErrReservationAlreadyCompleted is returned when completing an order whose
	// reservation is already COMPLETED (defensive; idempotency normally
	// short-circuits this).
	ErrReservationAlreadyCompleted = errors.New("reservation already completed")
	// ErrReservationAlreadyCancelled is returned when cancelling an order whose
	// reservation is already CANCELLED (defensive).
	ErrReservationAlreadyCancelled = errors.New("reservation already cancelled")
	// ErrDuplicateActiveReservation is returned when an order already has an
	// ACTIVE reservation batch (spec Sections 9, 21, 26).
	ErrDuplicateActiveReservation = errors.New("duplicate active reservation for order")
)

// Reservation is a commitment of available inventory to an order
// (spec Section 2.5). VariantID is populated by joins in the repository and is
// not persisted on the reservations row.
type Reservation struct {
	ID              uuid.UUID         `json:"id"`
	InventoryItemID uuid.UUID         `json:"inventory_item_id"`
	VariantID       uuid.UUID         `json:"variant_id"`
	LocationID      uuid.UUID         `json:"location_id"`
	OrderID         *uuid.UUID        `json:"order_id,omitempty"`
	Quantity        Quantity          `json:"quantity"`
	ReservedAt      time.Time         `json:"reserved_at"`
	ExpiresAt       *time.Time        `json:"expires_at,omitempty"`
	Status          ReservationStatus `json:"status"`
	ReleasedAt      *time.Time        `json:"released_at,omitempty"`
}

// ReservationItemInput is one line item of a reservation request
// (spec Section 8).
type ReservationItemInput struct {
	VariantID  uuid.UUID `json:"variant_id"`
	LocationID uuid.UUID `json:"location_id"`
	Quantity   int       `json:"quantity"`
}

// CreateReservationInput is the request payload for
// POST /inventory/reservations (spec Section 8).
type CreateReservationInput struct {
	OrderID   uuid.UUID              `json:"order_id"`
	ExpiresAt *time.Time             `json:"expires_at"`
	Items     []ReservationItemInput `json:"items"`
}

// ReservationResult is the order-level outcome of create/complete/cancel,
// carrying the affected reservation rows (spec Sections 11, 14, 17).
type ReservationResult struct {
	OrderID      uuid.UUID         `json:"order_id"`
	Status       ReservationStatus `json:"status"`
	Reservations []Reservation     `json:"reservations"`
}

// ReservationUseCase is the application-layer contract for managing order
// reservations (spec Sections 7-17). All operations are idempotent and
// transactional.
type ReservationUseCase interface {
	Create(ctx context.Context, input CreateReservationInput) (ReservationResult, error)
	Complete(ctx context.Context, orderID uuid.UUID) (ReservationResult, error)
	Cancel(ctx context.Context, orderID uuid.UUID) (ReservationResult, error)
}

// ReservationExpiryUseCase is the application-layer contract for the background
// expiration worker (spec Section 18). ExpireDue returns the number of
// reservations expired.
type ReservationExpiryUseCase interface {
	ExpireDue(ctx context.Context) (int, error)
}
