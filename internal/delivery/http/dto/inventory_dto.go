package dto

import (
	"strings"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// CreateReservationRequest is the public POST /api/v1/inventory/reservations
// request body. Item IDs are product-variant identifiers, never internal
// inventory-item identifiers.
type CreateReservationRequest struct {
	OrderID   string                      `json:"orderId" binding:"required,uuid"`
	ExpiresAt string                      `json:"expiresAt" binding:"required"`
	Items     []ReservationRequestItemDTO `json:"items" binding:"required,min=1,dive"`
}

// ReservationRequestItemDTO is one requested checkout item.
type ReservationRequestItemDTO struct {
	ID  string `json:"id" binding:"required,uuid"`
	Qty int    `json:"qty" binding:"required,min=1"`
}

// ToDomain converts the request DTO to the domain input. It validates that
// every identifier is a real UUID and that expiresAt is a well-formed RFC 3339
// instant with an explicit offset and at most six fractional-second digits,
// normalized to UTC. Malformed or unsupported expiry values return
// domain.ErrInvalidExpiry so the delivery layer can distinguish them from
// other validation failures.
func (r CreateReservationRequest) ToDomain() (uuid.UUID, time.Time, []domain.ReservationRequestItem, error) {
	orderID, err := uuid.Parse(r.OrderID)
	if err != nil {
		return uuid.Nil, time.Time{}, nil, err
	}

	expiresAt, err := ParseExpiry(r.ExpiresAt)
	if err != nil {
		return uuid.Nil, time.Time{}, nil, domain.ErrInvalidExpiry
	}

	items := make([]domain.ReservationRequestItem, 0, len(r.Items))
	for _, it := range r.Items {
		variantID, err := uuid.Parse(it.ID)
		if err != nil {
			return uuid.Nil, time.Time{}, nil, err
		}
		items = append(items, domain.ReservationRequestItem{
			VariantID: variantID,
			Quantity:  domain.Quantity(it.Qty),
		})
	}
	return orderID, expiresAt, items, nil
}

// ParseExpiry parses an RFC 3339 timestamp with an explicit UTC offset and no
// more than six fractional-second digits, and normalizes it to UTC. A naive
// timestamp (no offset), a malformed value, or more than six fractional digits
// is rejected. Equivalent offset representations normalize to the same UTC
// instant.
func ParseExpiry(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	if fractionalDigits(s) > 6 {
		return time.Time{}, errExpiryPrecision
	}
	return t.UTC(), nil
}

// errExpiryPrecision marks an expiry whose fractional-second precision exceeds
// what PostgreSQL timestamptz can store losslessly.
type expiryPrecisionError struct{}

func (expiryPrecisionError) Error() string {
	return "expiresAt must not have more than six fractional-second digits"
}

var errExpiryPrecision error = expiryPrecisionError{}

// fractionalDigits returns the number of digits in the fractional-second part
// of a RFC 3339 timestamp, or zero when none is present.
func fractionalDigits(s string) int {
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return 0
	}
	n := 0
	for i++; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n++
	}
	return n
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
// success envelope. Expiry instants are serialized with RFC3339Nano so a
// stored microsecond instant is never truncated.
func ToReservationResponse(result domain.ReservationResult) ReservationSuccessResponse {
	items := make([]ReservationItemData, 0, len(result.Items))
	for _, it := range result.Items {
		items = append(items, ReservationItemData{
			ReservationID: it.ReservationID.String(),
			VariantID:     it.VariantID.String(),
			Qty:           it.Quantity.Int(),
			Status:        string(it.Status),
			ExpiresAt:     it.ExpiresAt.Format(time.RFC3339Nano),
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
