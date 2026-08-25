package dto

import (
	"encoding/json"
	"strings"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// NullableUUID wraps *uuid.UUID to handle empty strings `""` as nil during JSON binding.
type NullableUUID struct {
	*uuid.UUID
}

// UnmarshalJSON handles converting JSON strings, empty strings, and null into *uuid.UUID.
func (nu *NullableUUID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// If it's explicit null, json.Unmarshal returns nil and s remains ""
		if string(b) == "null" {
			nu.UUID = nil
			return nil
		}
		return err
	}

	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		nu.UUID = nil
		return nil
	}

	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return err
	}

	nu.UUID = &parsed
	return nil
}

// MarshalJSON ensures the type marshals cleanly back to JSON.
func (nu NullableUUID) MarshalJSON() ([]byte, error) {
	if nu.UUID == nil {
		return []byte("null"), nil
	}
	return json.Marshal(nu.UUID.String())
}

// CreateStockMoveRequest is the request body for POST /inventory/stock-moves
// (spec Section 4.1).
//
//	type CreateStockMoveRequest struct {
//		VariantID      uuid.UUID            `json:"variant_id" binding:"required" validate:"required"`
//		MoveType       domain.StockMoveType `json:"move_type" binding:"required" validate:"required,oneof=IN OUT TRANSFER ADJUST"`
//		Quantity       int                  `json:"quantity" binding:"required" validate:"required,gt=0"`
//		FromLocationID *uuid.UUID           `json:"from_location_id" validate:"omitempty"`
//		ToLocationID   *uuid.UUID           `json:"to_location_id" validate:"omitempty"`
//		Reason         *string              `json:"reason"`
//		CreatedBy      *uuid.UUID           `json:"created_by" validate:"omitempty"`
//	}
type CreateStockMoveRequest struct {
	VariantID      uuid.UUID            `json:"variant_id" binding:"required"`
	MoveType       domain.StockMoveType `json:"move_type" binding:"required,oneof=IN OUT TRANSFER ADJUST"`
	Quantity       int                  `json:"quantity" binding:"required,gt=0"`
	FromLocationID NullableUUID         `json:"from_location_id" binding:"omitempty"`
	ToLocationID   NullableUUID         `json:"to_location_id" binding:"omitempty"`
	Reason         *string              `json:"reason" binding:"omitempty"`
	CreatedBy      NullableUUID         `json:"created_by" binding:"omitempty"`
}

func (r CreateStockMoveRequest) ToDomain() domain.CreateStockMoveInput {
	return domain.CreateStockMoveInput{
		VariantID:      r.VariantID,
		MoveType:       r.MoveType,
		Quantity:       r.Quantity,
		FromLocationID: r.FromLocationID.UUID,
		ToLocationID:   r.ToLocationID.UUID,
		Reason:         r.Reason,
		CreatedBy:      r.CreatedBy.UUID,
	}
}

// ReservationItemRequest is one line item of a reservation request
// (spec Section 8). The caller does not supply a location — the server picks
// the location to reserve from.
type ReservationItemRequest struct {
	VariantID uuid.UUID `json:"variant_id" binding:"required" validate:"required"`
	Quantity  int       `json:"quantity" binding:"required" validate:"required,gt=0"`
}

func (r ReservationItemRequest) ToDomain() domain.ReservationItemInput {
	return domain.ReservationItemInput{
		VariantID: r.VariantID,
		Quantity:  r.Quantity,
	}
}

// CreateReservationRequest is the request body for POST /inventory/reservations
// (spec Section 8).
type CreateReservationRequest struct {
	OrderID   uuid.UUID                `json:"order_id" binding:"required" validate:"required"`
	ExpiresAt *time.Time               `json:"expires_at" validate:"omitempty"`
	Items     []ReservationItemRequest `json:"items" binding:"required" validate:"required,min=1,dive"`
}

func (r CreateReservationRequest) ToDomain() domain.CreateReservationInput {
	items := make([]domain.ReservationItemInput, 0, len(r.Items))
	for _, it := range r.Items {
		items = append(items, it.ToDomain())
	}
	return domain.CreateReservationInput{
		OrderID:   r.OrderID,
		ExpiresAt: r.ExpiresAt,
		Items:     items,
	}
}

// Response shapes (spec Sections 6, 11, 14, 17).

type StockMoveData struct {
	MoveID         uuid.UUID            `json:"move_id"`
	VariantID      uuid.UUID            `json:"variant_id"`
	MoveType       domain.StockMoveType `json:"move_type"`
	FromLocationID *uuid.UUID           `json:"from_location_id"`
	ToLocationID   *uuid.UUID           `json:"to_location_id"`
	Quantity       int                  `json:"quantity"`
	Reason         *string              `json:"reason,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
}

func ToStockMoveData(m domain.StockMove, variantID uuid.UUID) StockMoveData {
	return StockMoveData{
		MoveID:         m.ID,
		VariantID:      variantID,
		MoveType:       m.MoveType,
		FromLocationID: m.FromLocationID,
		ToLocationID:   m.ToLocationID,
		Quantity:       m.Quantity.Int(),
		Reason:         m.Reason,
		CreatedAt:      m.CreatedAt,
	}
}

type ReservationData struct {
	ReservationID uuid.UUID  `json:"reservation_id"`
	VariantID     uuid.UUID  `json:"variant_id"`
	LocationID    uuid.UUID  `json:"location_id"`
	Quantity      int        `json:"quantity"`
	Status        string     `json:"status"`
	ReservedAt    time.Time  `json:"reserved_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	ReleasedAt    *time.Time `json:"released_at,omitempty"`
}

func ToReservationData(r domain.Reservation) ReservationData {
	return ReservationData{
		ReservationID: r.ID,
		VariantID:     r.VariantID,
		LocationID:    r.LocationID,
		Quantity:      r.Quantity.Int(),
		Status:        string(r.Status),
		ReservedAt:    r.ReservedAt,
		ExpiresAt:     r.ExpiresAt,
		ReleasedAt:    r.ReleasedAt,
	}
}

type ReservationResultData struct {
	OrderID      uuid.UUID         `json:"order_id"`
	Status       string            `json:"status"`
	Reservations []ReservationData `json:"reservations"`
}

func ToReservationResultData(res domain.ReservationResult) ReservationResultData {
	items := make([]ReservationData, 0, len(res.Reservations))
	for _, r := range res.Reservations {
		items = append(items, ToReservationData(r))
	}
	return ReservationResultData{
		OrderID:      res.OrderID,
		Status:       string(res.Status),
		Reservations: items,
	}
}
