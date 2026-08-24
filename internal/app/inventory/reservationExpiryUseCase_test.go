package inventory

import (
	"context"
	"testing"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

func TestExpiry_ExpiresDueReservations(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	r := seedActiveReservation(repo, orderID, va, ia, la, 3, 7, 3)
	expiredAt := time.Now().Add(-time.Hour)
	r.ExpiresAt = &expiredAt
	// seedActiveReservation appended r; update the stored copy with ExpiresAt.
	repo.reservations[len(repo.reservations)-1].ExpiresAt = &expiredAt

	uc := NewReservationExpiryUseCase(repo)
	count, err := uc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expired count = %d, want 1", count)
	}

	level, _ := repo.level(ia, la)
	if level.AvailableQty.Int() != 10 || level.ReservedQty.Int() != 0 {
		t.Fatalf("after expiry: available=%d reserved=%d, want 10/0", level.AvailableQty.Int(), level.ReservedQty.Int())
	}
	if repo.reservations[0].Status != domain.ReservationExpired {
		t.Fatalf("reservation status = %s, want EXPIRED", repo.reservations[0].Status)
	}
}

func TestExpiry_IgnoresFutureReservations(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	r := seedActiveReservation(repo, orderID, va, ia, la, 2, 5, 2)
	future := time.Now().Add(time.Hour)
	r.ExpiresAt = &future
	repo.reservations[len(repo.reservations)-1].ExpiresAt = &future

	uc := NewReservationExpiryUseCase(repo)
	count, err := uc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired count = %d, want 0", count)
	}
	level, _ := repo.level(ia, la)
	if level.AvailableQty.Int() != 5 || level.ReservedQty.Int() != 2 {
		t.Fatalf("future reservation must be untouched: available=%d reserved=%d", level.AvailableQty.Int(), level.ReservedQty.Int())
	}
}
