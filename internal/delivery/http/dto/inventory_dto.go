package dto

import (
	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// CreateReservationRequest is the public POST /api/v1/inventory/reservations
// request body. Item IDs are product-variant identifiers, never internal
// inventory-item identifiers.
type CreateReservationRequest struct {
	OrderID string                      `json:"orderId" binding:"required,uuid"`
	Items   []ReservationRequestItemDTO `json:"items" binding:"required,min=1,dive"`
}

// ReservationRequestItemDTO is one requested checkout item.
type ReservationRequestItemDTO struct {
	ID  string `json:"id" binding:"required,uuid"`
	Qty int    `json:"qty" binding:"required,min=1"`
}

// ToDomain converts the request DTO to the domain input, validating that every
// identifier is a real UUID.
func (r CreateReservationRequest) ToDomain() (uuid.UUID, []domain.ReservationRequestItem, error) {
	orderID, err := uuid.Parse(r.OrderID)
	if err != nil {
		return uuid.Nil, nil, err
	}

	items := make([]domain.ReservationRequestItem, 0, len(r.Items))
	for _, it := range r.Items {
		variantID, err := uuid.Parse(it.ID)
		if err != nil {
			return uuid.Nil, nil, err
		}
		items = append(items, domain.ReservationRequestItem{
			VariantID: variantID,
			Quantity:  domain.Quantity(it.Qty),
		})
	}
	return orderID, items, nil
}

// ReservationSuccessResponse is the success envelope for created or retried
// reservations.
type ReservationSuccessResponse struct {
	Status string          `json:"status"`
	Data   ReservationData `json:"data"`
}

// ReservationData carries the order reference and held items.
type ReservationData struct {
	OrderID string                `json:"orderId"`
	Items   []ReservationItemData `json:"items"`
}

// ReservationItemData is one held item in the success response.
type ReservationItemData struct {
	ReservationID string `json:"reservationId"`
	VariantID     string `json:"variantId"`
	Qty           int    `json:"qty"`
	Status        string `json:"status"`
	ExpiresAt     string `json:"expiresAt"`
}

// ToReservationResponse maps a persisted reservation result to the public
// success envelope.
func ToReservationResponse(result domain.ReservationResult) ReservationSuccessResponse {
	items := make([]ReservationItemData, 0, len(result.Items))
	for _, it := range result.Items {
		items = append(items, ReservationItemData{
			ReservationID: it.ReservationID.String(),
			VariantID:     it.VariantID.String(),
			Qty:           it.Quantity.Int(),
			Status:        string(it.Status),
			ExpiresAt:     it.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return ReservationSuccessResponse{
		Status: "success",
		Data: ReservationData{
			OrderID: result.OrderID.String(),
			Items:   items,
		},
	}
}
