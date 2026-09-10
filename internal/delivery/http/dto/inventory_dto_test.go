package dto

import (
	"errors"
	"testing"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestParseExpiry_AcceptsExplicitOffsetAndNormalizesUTC(t *testing.T) {
	got, err := ParseExpiry("2030-01-02T03:04:05.123456+07:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2030, 1, 1, 20, 4, 5, 123456000, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got.Location() != time.UTC {
		t.Fatalf("parsed expiry must be normalized to UTC, got %v", got.Location())
	}
}

func TestParseExpiry_EquivalentOffsetsNormalizeToSameInstant(t *testing.T) {
	a, err := ParseExpiry("2030-01-02T03:04:05.123456+07:00")
	if err != nil {
		t.Fatalf("parse +07:00: %v", err)
	}
	b, err := ParseExpiry("2030-01-01T20:04:05.123456Z")
	if err != nil {
		t.Fatalf("parse Z: %v", err)
	}
	if !a.Equal(b) {
		t.Fatalf("equivalent offsets must normalize to the same instant: %v vs %v", a, b)
	}
}

func TestParseExpiry_RejectsUnsupportedInput(t *testing.T) {
	cases := []string{
		"",                               // absent
		"not-a-timestamp",                // malformed
		"2030-01-02T03:04:05",            // offset-less (naive)
		"2030-01-02T03:04:05.1234567Z",   // seven fractional digits
		"2030-01-02T03:04:05.123456789Z", // nine fractional digits
	}
	for _, in := range cases {
		if _, err := ParseExpiry(in); err == nil {
			t.Errorf("ParseExpiry(%q) must fail", in)
		}
	}
}

func TestCreateReservationRequest_ToDomain_MapsInvalidExpiry(t *testing.T) {
	req := CreateReservationRequest{
		OrderID:   uuid.NewString(),
		ExpiresAt: "2030-01-02T03:04:05", // offset-less
		Items:     []ReservationRequestItemDTO{{ID: uuid.NewString(), Qty: 1}},
	}
	_, _, _, err := req.ToDomain()
	if !errors.Is(err, domain.ErrInvalidExpiry) {
		t.Fatalf("got %v, want ErrInvalidExpiry", err)
	}
}

func TestCreateReservationRequest_ToDomain_PassesNormalizedExpiry(t *testing.T) {
	orderID := uuid.New()
	variantID := uuid.New()
	req := CreateReservationRequest{
		OrderID:   orderID.String(),
		ExpiresAt: "2030-01-02T03:04:05.123456+07:00",
		Items:     []ReservationRequestItemDTO{{ID: variantID.String(), Qty: 2}},
	}
	gotOrder, gotExpiry, items, err := req.ToDomain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotOrder != orderID {
		t.Fatalf("order id = %v, want %v", gotOrder, orderID)
	}
	want := time.Date(2030, 1, 1, 20, 4, 5, 123456000, time.UTC)
	if !gotExpiry.Equal(want) {
		t.Fatalf("expiry = %v, want %v", gotExpiry, want)
	}
	if len(items) != 1 || items[0].VariantID != variantID || items[0].Quantity != 2 {
		t.Fatalf("items mismatch: %v", items)
	}
}

func TestToReservationResponse_UsesRFC3339Nano(t *testing.T) {
	reservationID := uuid.New()
	variantID := uuid.New()
	// A microsecond instant must not be truncated to seconds in the response.
	expires := time.Date(2030, 1, 1, 20, 4, 5, 123456000, time.UTC)

	resp := ToReservationResponse(domain.ReservationResult{
		OrderID: uuid.New(),
		Items: []domain.ReservationItemResult{
			{ReservationID: reservationID, VariantID: variantID, Quantity: 2, Status: domain.ReservationActive, ExpiresAt: expires},
		},
	})

	if resp.Status != "success" || len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected envelope: %+v", resp)
	}
	item := resp.Data.Items[0]
	if item.ExpiresAt != "2030-01-01T20:04:05.123456Z" {
		t.Fatalf("expiresAt = %q, want microsecond RFC3339Nano output", item.ExpiresAt)
	}
	if item.ReservationID != reservationID.String() || item.VariantID != variantID.String() || item.Qty != 2 || item.Status != "ACTIVE" {
		t.Fatalf("item mismatch: %+v", item)
	}
}
