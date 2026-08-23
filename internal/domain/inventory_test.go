package domain

import "testing"

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
