package domain

import (
	"testing"

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
