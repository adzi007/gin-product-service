package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInventoryLevelReserve(t *testing.T) {
	l := InventoryLevel{AvailableQty: 10, ReservedQty: 0}

	if err := l.Reserve(4); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.AvailableQty != 6 || l.ReservedQty != 4 {
		t.Fatalf("got AvailableQty=%d ReservedQty=%d, want 6/4", l.AvailableQty, l.ReservedQty)
	}

	if err := l.Reserve(100); err != ErrInsufficientStock {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}
	if err := l.Reserve(-1); err != ErrInvalidQuantity {
		t.Fatalf("got %v, want ErrInvalidQuantity", err)
	}
}

// TestInventoryLevelReserve_AvailableToReservedTransfer confirms the transfer
// invariant: available decreases and reserved increases by exactly the held
// quantity, and the total never changes.
func TestInventoryLevelReserve_AvailableToReservedTransfer(t *testing.T) {
	l := InventoryLevel{AvailableQty: 25, ReservedQty: 5}
	before := l.AvailableQty + l.ReservedQty

	if err := l.Reserve(10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.AvailableQty != 15 || l.ReservedQty != 15 {
		t.Fatalf("got AvailableQty=%d ReservedQty=%d, want 15/15", l.AvailableQty, l.ReservedQty)
	}
	if l.AvailableQty+l.ReservedQty != before {
		t.Fatalf("transfer must preserve total quantity: before=%d after=%d", before, l.AvailableQty+l.ReservedQty)
	}
}

func TestValidateReservationItems_RejectsEmpty(t *testing.T) {
	if err := ValidateReservationItems(nil); err != ErrReservationValidation {
		t.Fatalf("got %v, want ErrReservationValidation", err)
	}
	if err := ValidateReservationItems([]ReservationRequestItem{}); err != ErrReservationValidation {
		t.Fatalf("got %v, want ErrReservationValidation", err)
	}
}

func TestValidateReservationItems_RejectsNonPositiveOrNilIDs(t *testing.T) {
	cases := [][]ReservationRequestItem{
		{{VariantID: uuid.New(), Quantity: 0}},
		{{VariantID: uuid.New(), Quantity: -1}},
		{{VariantID: uuid.Nil, Quantity: 1}},
	}
	for _, items := range cases {
		if err := ValidateReservationItems(items); err != ErrReservationValidation {
			t.Fatalf("items %v: got %v, want ErrReservationValidation", items, err)
		}
	}
}

func TestValidateReservationItems_RejectsDuplicateVariants(t *testing.T) {
	id := uuid.New()
	items := []ReservationRequestItem{
		{VariantID: id, Quantity: 1},
		{VariantID: id, Quantity: 2},
	}
	if err := ValidateReservationItems(items); err != ErrReservationValidation {
		t.Fatalf("got %v, want ErrReservationValidation", err)
	}
}

func TestValidateReservationItems_AcceptsPositiveUniqueItems(t *testing.T) {
	items := []ReservationRequestItem{
		{VariantID: uuid.New(), Quantity: 1},
		{VariantID: uuid.New(), Quantity: 3},
	}
	if err := ValidateReservationItems(items); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErrInvalidAndExpiredExpiryAreDistinct(t *testing.T) {
	if ErrInvalidExpiry == nil || ErrExpiredExpiry == nil {
		t.Fatal("expiry errors must be defined")
	}
	if ErrInvalidExpiry == ErrExpiredExpiry {
		t.Fatal("invalid and expired expiry errors must be distinct sentinels")
	}
	if ErrInvalidExpiry.Error() == ErrExpiredExpiry.Error() {
		t.Fatal("invalid and expired expiry errors must carry distinct messages")
	}
	if ErrInvalidExpiry == ErrReservationValidation || ErrExpiredExpiry == ErrReservationConflict {
		t.Fatal("expiry errors must not alias existing validation/conflict errors")
	}
}

func TestCreateReservationInputCarriesCallerExpiry(t *testing.T) {
	expires := time.Date(2030, 1, 1, 20, 4, 5, 123456000, time.UTC)
	input := CreateReservationInput{
		OrderID:   uuid.New(),
		ExpiresAt: expires,
		Items:     []CreateReservationItem{{VariantID: uuid.New(), Quantity: 1}},
	}
	if !input.ExpiresAt.Equal(expires) {
		t.Fatalf("input must retain the caller expiry unchanged: %v", input.ExpiresAt)
	}
}

func TestValidateExpiry_StrictlyFuture(t *testing.T) {
	reference := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	if err := ValidateExpiry(reference.Add(time.Second), reference); err != nil {
		t.Fatalf("future expiry must pass: %v", err)
	}
	if err := ValidateExpiry(reference, reference); err != ErrExpiredExpiry {
		t.Fatalf("equal instant got %v, want ErrExpiredExpiry", err)
	}
	if err := ValidateExpiry(reference.Add(-time.Second), reference); err != ErrExpiredExpiry {
		t.Fatalf("past instant got %v, want ErrExpiredExpiry", err)
	}
}

func TestValidateExpiry_EquivalentOffsetInstantsCompareEqual(t *testing.T) {
	// The same instant expressed with different offsets must compare equal so
	// equivalent-offset retries identify the same expiry.
	plusSeven := time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.FixedZone("+07", 7*3600))
	utc := time.Date(2030, 1, 1, 20, 4, 5, 123456000, time.UTC)
	if !plusSeven.Equal(utc) {
		t.Fatal("equivalent offset representations must represent the same instant")
	}
}
