package inventory

import (
	"context"
	"testing"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// setupReservable creates a variant/item/location and an inventory level, and
// returns the ids for use in tests.
func setupReservable(repo *fakeInventoryRepo) (variantID, itemID, locationID uuid.UUID) {
	variantID, itemID, locationID = uuid.New(), uuid.New(), uuid.New()
	repo.addVariant(variantID, itemID)
	repo.addLocation(locationID)
	repo.addLevel(itemID, locationID, 0, 0)
	return
}

// seedActiveReservation inserts an ACTIVE reservation directly plus a level
// with the given available/reserved quantities, simulating a prior reservation.
func seedActiveReservation(repo *fakeInventoryRepo, orderID uuid.UUID, variantID, itemID, locationID uuid.UUID, qty, available, reserved int) domain.Reservation {
	repo.levels[levelKey{itemID, locationID}] = domain.InventoryLevel{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: itemID,
		LocationID:      locationID,
		AvailableQty:    domain.Quantity(available),
		ReservedQty:     domain.Quantity(reserved),
		UpdatedAt:       time.Now().UTC(),
	}
	r := domain.Reservation{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: itemID,
		VariantID:       variantID,
		LocationID:      locationID,
		OrderID:         &orderID,
		Quantity:        domain.Quantity(qty),
		ReservedAt:      time.Now().UTC(),
		Status:          domain.ReservationActive,
	}
	repo.reservations = append(repo.reservations, r)
	return r
}

func TestReservation_CreateMultiItem(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	vb, ib, lb := setupReservable(repo)
	repo.levels[levelKey{ia, la}] = domain.InventoryLevel{ID: uuid.Must(uuid.NewV7()), InventoryItemID: ia, LocationID: la, AvailableQty: 10, ReservedQty: 0, UpdatedAt: time.Now().UTC()}
	repo.levels[levelKey{ib, lb}] = domain.InventoryLevel{ID: uuid.Must(uuid.NewV7()), InventoryItemID: ib, LocationID: lb, AvailableQty: 5, ReservedQty: 0, UpdatedAt: time.Now().UTC()}

	uc := NewReservationUseCase(repo)
	result, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: orderID,
		Items: []domain.ReservationItemInput{
			{VariantID: va, Quantity: 3},
			{VariantID: vb, Quantity: 2},
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if result.Status != domain.ReservationActive {
		t.Fatalf("result status = %s, want ACTIVE", result.Status)
	}
	if len(result.Reservations) != 2 {
		t.Fatalf("expected 2 reservations, got %d", len(result.Reservations))
	}

	laLevel, _ := repo.level(ia, la)
	lbLevel, _ := repo.level(ib, lb)
	if laLevel.AvailableQty.Int() != 7 || laLevel.ReservedQty.Int() != 3 {
		t.Fatalf("A: available=%d reserved=%d, want 7/3", laLevel.AvailableQty.Int(), laLevel.ReservedQty.Int())
	}
	if lbLevel.AvailableQty.Int() != 3 || lbLevel.ReservedQty.Int() != 2 {
		t.Fatalf("B: available=%d reserved=%d, want 3/2", lbLevel.AvailableQty.Int(), lbLevel.ReservedQty.Int())
	}

	if len(repo.reservations) != 2 {
		t.Fatalf("expected 2 reservation rows, got %d", len(repo.reservations))
	}
	for _, r := range repo.reservations {
		if r.Status != domain.ReservationActive {
			t.Fatalf("reservation status = %s, want ACTIVE", r.Status)
		}
		if r.OrderID == nil || *r.OrderID != orderID {
			t.Fatalf("reservation order_id mismatch")
		}
		if r.ID == uuid.Nil {
			t.Fatalf("reservation id must be generated")
		}
	}
}

func TestReservation_CreateOneItemInsufficientRollsBackAll(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	vb, ib, lb := setupReservable(repo)
	repo.levels[levelKey{ia, la}] = domain.InventoryLevel{ID: uuid.Must(uuid.NewV7()), InventoryItemID: ia, LocationID: la, AvailableQty: 10, ReservedQty: 0, UpdatedAt: time.Now().UTC()}
	repo.levels[levelKey{ib, lb}] = domain.InventoryLevel{ID: uuid.Must(uuid.NewV7()), InventoryItemID: ib, LocationID: lb, AvailableQty: 1, ReservedQty: 0, UpdatedAt: time.Now().UTC()}

	uc := NewReservationUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: orderID,
		Items: []domain.ReservationItemInput{
			{VariantID: va, Quantity: 3},
			{VariantID: vb, Quantity: 2}, // B only has 1
		},
	})
	if err != domain.ErrInsufficientStock {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}

	// No partial state: nothing reserved, no reservation rows, no level changes.
	laLevel, _ := repo.level(ia, la)
	lbLevel, _ := repo.level(ib, lb)
	if laLevel.AvailableQty.Int() != 10 || laLevel.ReservedQty.Int() != 0 {
		t.Fatalf("A must be untouched: available=%d reserved=%d", laLevel.AvailableQty.Int(), laLevel.ReservedQty.Int())
	}
	if lbLevel.AvailableQty.Int() != 1 || lbLevel.ReservedQty.Int() != 0 {
		t.Fatalf("B must be untouched: available=%d reserved=%d", lbLevel.AvailableQty.Int(), lbLevel.ReservedQty.Int())
	}
	if len(repo.reservations) != 0 {
		t.Fatalf("expected no reservation rows, got %d", len(repo.reservations))
	}
}

func TestReservation_CreateIdempotent(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	repo.levels[levelKey{ia, la}] = domain.InventoryLevel{ID: uuid.Must(uuid.NewV7()), InventoryItemID: ia, LocationID: la, AvailableQty: 10, ReservedQty: 0, UpdatedAt: time.Now().UTC()}

	uc := NewReservationUseCase(repo)
	input := domain.CreateReservationInput{
		OrderID: orderID,
		Items:   []domain.ReservationItemInput{{VariantID: va, Quantity: 3}},
	}

	first, err := uc.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	levelAfterFirst, _ := repo.level(ia, la)
	if levelAfterFirst.AvailableQty.Int() != 7 {
		t.Fatalf("after first create available = %d, want 7", levelAfterFirst.AvailableQty.Int())
	}

	// Retry (e.g. after a network timeout): must return the existing batch.
	second, err := uc.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("retry create failed: %v", err)
	}
	if len(second.Reservations) != len(first.Reservations) {
		t.Fatalf("retry returned %d reservations, want %d", len(second.Reservations), len(first.Reservations))
	}
	if second.Reservations[0].ID != first.Reservations[0].ID {
		t.Fatalf("retry must return the same reservation ids")
	}

	levelAfterRetry, _ := repo.level(ia, la)
	if levelAfterRetry.AvailableQty.Int() != 7 || levelAfterRetry.ReservedQty.Int() != 3 {
		t.Fatalf("retry must not double-reserve: available=%d reserved=%d", levelAfterRetry.AvailableQty.Int(), levelAfterRetry.ReservedQty.Int())
	}
	if len(repo.reservations) != 1 {
		t.Fatalf("expected exactly 1 reservation row, got %d", len(repo.reservations))
	}
}

func TestReservation_Complete(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	seedActiveReservation(repo, orderID, va, ia, la, 3, 7, 3)

	uc := NewReservationUseCase(repo)
	result, err := uc.Complete(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if result.Status != domain.ReservationCompleted {
		t.Fatalf("status = %s, want COMPLETED", result.Status)
	}
	level, _ := repo.level(ia, la)
	if level.AvailableQty.Int() != 7 || level.ReservedQty.Int() != 0 {
		t.Fatalf("after complete: available=%d reserved=%d, want 7/0", level.AvailableQty.Int(), level.ReservedQty.Int())
	}
	if repo.reservations[0].Status != domain.ReservationCompleted {
		t.Fatalf("reservation status = %s, want COMPLETED", repo.reservations[0].Status)
	}
}

func TestReservation_DoubleCompleteIsNoOp(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	seedActiveReservation(repo, orderID, va, ia, la, 3, 7, 3)

	uc := NewReservationUseCase(repo)
	if _, err := uc.Complete(context.Background(), orderID); err != nil {
		t.Fatalf("first complete failed: %v", err)
	}

	// Second complete: idempotent no-op returning current state.
	result, err := uc.Complete(context.Background(), orderID)
	if err != nil {
		t.Fatalf("second complete must not error: %v", err)
	}
	if result.Status != domain.ReservationCompleted {
		t.Fatalf("second complete status = %s, want COMPLETED", result.Status)
	}
	level, _ := repo.level(ia, la)
	if level.AvailableQty.Int() != 7 || level.ReservedQty.Int() != 0 {
		t.Fatalf("double complete must not consume stock twice: available=%d reserved=%d", level.AvailableQty.Int(), level.ReservedQty.Int())
	}
}

func TestReservation_Cancel(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	seedActiveReservation(repo, orderID, va, ia, la, 3, 7, 3)

	uc := NewReservationUseCase(repo)
	result, err := uc.Cancel(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if result.Status != domain.ReservationCancelled {
		t.Fatalf("status = %s, want CANCELLED", result.Status)
	}
	level, _ := repo.level(ia, la)
	if level.AvailableQty.Int() != 10 || level.ReservedQty.Int() != 0 {
		t.Fatalf("after cancel: available=%d reserved=%d, want 10/0", level.AvailableQty.Int(), level.ReservedQty.Int())
	}
	if repo.reservations[0].Status != domain.ReservationCancelled {
		t.Fatalf("reservation status = %s, want CANCELLED", repo.reservations[0].Status)
	}
}

func TestReservation_DoubleCancelIsNoOp(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	va, ia, la := setupReservable(repo)
	seedActiveReservation(repo, orderID, va, ia, la, 3, 7, 3)

	uc := NewReservationUseCase(repo)
	if _, err := uc.Cancel(context.Background(), orderID); err != nil {
		t.Fatalf("first cancel failed: %v", err)
	}

	result, err := uc.Cancel(context.Background(), orderID)
	if err != nil {
		t.Fatalf("second cancel must not error: %v", err)
	}
	if result.Status != domain.ReservationCancelled {
		t.Fatalf("second cancel status = %s, want CANCELLED", result.Status)
	}
	level, _ := repo.level(ia, la)
	if level.AvailableQty.Int() != 10 || level.ReservedQty.Int() != 0 {
		t.Fatalf("double cancel must not restore stock twice: available=%d reserved=%d", level.AvailableQty.Int(), level.ReservedQty.Int())
	}
}

func TestReservation_CompleteOrCancelWithoutReservations(t *testing.T) {
	repo := newFakeInventoryRepo()
	uc := NewReservationUseCase(repo)

	if _, err := uc.Complete(context.Background(), uuid.New()); err != domain.ErrReservationNotFound {
		t.Fatalf("Complete: got %v, want ErrReservationNotFound", err)
	}
	if _, err := uc.Cancel(context.Background(), uuid.New()); err != domain.ErrReservationNotFound {
		t.Fatalf("Cancel: got %v, want ErrReservationNotFound", err)
	}
}

func TestReservation_Validation(t *testing.T) {
	repo := newFakeInventoryRepo()
	uc := NewReservationUseCase(repo)

	items := []domain.ReservationItemInput{{VariantID: uuid.New(), Quantity: 1}}
	cases := []struct {
		name  string
		input domain.CreateReservationInput
	}{
		{"nil order", domain.CreateReservationInput{Items: items}},
		{"empty items", domain.CreateReservationInput{OrderID: uuid.New()}},
		{"nil variant", domain.CreateReservationInput{OrderID: uuid.New(), Items: []domain.ReservationItemInput{{Quantity: 1}}}},
		{"zero quantity", domain.CreateReservationInput{OrderID: uuid.New(), Items: []domain.ReservationItemInput{{VariantID: uuid.New(), Quantity: 0}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := uc.Create(context.Background(), tc.input); err != domain.ErrInvalidReservationInput {
				t.Fatalf("got %v, want ErrInvalidReservationInput", err)
			}
		})
	}
}

// setupMultiLocationItem creates a variant/item with inventory levels at two
// locations and returns the ids.
func setupMultiLocationItem(repo *fakeInventoryRepo, defaultLoc *uuid.UUID, qtyByLoc map[uuid.UUID]int) (variantID, itemID uuid.UUID) {
	variantID, itemID = uuid.New(), uuid.New()
	repo.addVariant(variantID, itemID)
	for loc, qty := range qtyByLoc {
		if defaultLoc != nil && loc == *defaultLoc {
			repo.addDefaultLocation(loc)
		} else {
			repo.addLocation(loc)
		}
		repo.addLevel(itemID, loc, qty, 0)
	}
	return
}

// TestReservation_CreatePicksDefaultLocationWithEnoughStock verifies the
// selection rule prefers the default location even when another location has
// more stock (spec: remove-location-from-reservation.md Section 2).
func TestReservation_CreatePicksDefaultLocationWithEnoughStock(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	defaultLoc, otherLoc := uuid.New(), uuid.New()
	variantID, itemID := setupMultiLocationItem(repo, &defaultLoc, map[uuid.UUID]int{defaultLoc: 5, otherLoc: 10})

	uc := NewReservationUseCase(repo)
	result, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: orderID,
		Items:   []domain.ReservationItemInput{{VariantID: variantID, Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if len(result.Reservations) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(result.Reservations))
	}
	if result.Reservations[0].LocationID != defaultLoc {
		t.Fatalf("location = %v, want default %v", result.Reservations[0].LocationID, defaultLoc)
	}
	def, _ := repo.level(itemID, defaultLoc)
	if def.AvailableQty.Int() != 3 || def.ReservedQty.Int() != 2 {
		t.Fatalf("default: available=%d reserved=%d, want 3/2", def.AvailableQty.Int(), def.ReservedQty.Int())
	}
	other, _ := repo.level(itemID, otherLoc)
	if other.AvailableQty.Int() != 10 || other.ReservedQty.Int() != 0 {
		t.Fatalf("other must be untouched: available=%d reserved=%d", other.AvailableQty.Int(), other.ReservedQty.Int())
	}
}

// TestReservation_CreatePicksHighestAvailableWhenNoDefault verifies that with
// no default location, the highest available_qty location wins.
func TestReservation_CreatePicksHighestAvailableWhenNoDefault(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	lowLoc, highLoc := uuid.New(), uuid.New()
	variantID, itemID := setupMultiLocationItem(repo, nil, map[uuid.UUID]int{lowLoc: 3, highLoc: 8})

	uc := NewReservationUseCase(repo)
	result, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: orderID,
		Items:   []domain.ReservationItemInput{{VariantID: variantID, Quantity: 4}},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if len(result.Reservations) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(result.Reservations))
	}
	if result.Reservations[0].LocationID != highLoc {
		t.Fatalf("location = %v, want highest-available %v", result.Reservations[0].LocationID, highLoc)
	}
	high, _ := repo.level(itemID, highLoc)
	if high.AvailableQty.Int() != 4 || high.ReservedQty.Int() != 4 {
		t.Fatalf("high: available=%d reserved=%d, want 4/4", high.AvailableQty.Int(), high.ReservedQty.Int())
	}
}

// TestReservation_CreateDefaultLacksStockFallsBack verifies that when the
// default location lacks enough stock, the highest available_qty location is
// used instead.
func TestReservation_CreateDefaultLacksStockFallsBack(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	defaultLoc, otherLoc := uuid.New(), uuid.New()
	variantID, itemID := setupMultiLocationItem(repo, &defaultLoc, map[uuid.UUID]int{defaultLoc: 2, otherLoc: 8})

	uc := NewReservationUseCase(repo)
	result, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: orderID,
		Items:   []domain.ReservationItemInput{{VariantID: variantID, Quantity: 5}},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if result.Reservations[0].LocationID != otherLoc {
		t.Fatalf("location = %v, want %v", result.Reservations[0].LocationID, otherLoc)
	}
	other, _ := repo.level(itemID, otherLoc)
	if other.AvailableQty.Int() != 3 || other.ReservedQty.Int() != 5 {
		t.Fatalf("other: available=%d reserved=%d, want 3/5", other.AvailableQty.Int(), other.ReservedQty.Int())
	}
}

// TestReservation_CreateTieBreaksByLocationID verifies deterministic selection
// between two locations with equal available_qty.
func TestReservation_CreateTieBreaksByLocationID(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	locA, locB := uuid.New(), uuid.New()
	if locB.String() < locA.String() {
		locA, locB = locB, locA
	}
	variantID, _ := setupMultiLocationItem(repo, nil, map[uuid.UUID]int{locA: 5, locB: 5})

	uc := NewReservationUseCase(repo)
	result, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: orderID,
		Items:   []domain.ReservationItemInput{{VariantID: variantID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if result.Reservations[0].LocationID != locA {
		t.Fatalf("location = %v, want lower location_id %v", result.Reservations[0].LocationID, locA)
	}
}

// TestReservation_CreateNoSingleLocationEnoughStockFails verifies that when no
// single location covers the requested quantity (even though the sum across
// locations would), the reservation fails with ErrInsufficientStock and nothing
// is committed (spec Section 4).
func TestReservation_CreateNoSingleLocationEnoughStockFails(t *testing.T) {
	repo := newFakeInventoryRepo()
	orderID := uuid.New()
	locA, locB := uuid.New(), uuid.New()
	variantID, itemID := setupMultiLocationItem(repo, nil, map[uuid.UUID]int{locA: 3, locB: 3})

	uc := NewReservationUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: orderID,
		Items:   []domain.ReservationItemInput{{VariantID: variantID, Quantity: 5}},
	})
	if err != domain.ErrInsufficientStock {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}
	if len(repo.reservations) != 0 {
		t.Fatalf("expected no reservation rows, got %d", len(repo.reservations))
	}
	if a, _ := repo.level(itemID, locA); a.AvailableQty.Int() != 3 || a.ReservedQty.Int() != 0 {
		t.Fatalf("A must be untouched: available=%d reserved=%d", a.AvailableQty.Int(), a.ReservedQty.Int())
	}
	if b, _ := repo.level(itemID, locB); b.AvailableQty.Int() != 3 || b.ReservedQty.Int() != 0 {
		t.Fatalf("B must be untouched: available=%d reserved=%d", b.AvailableQty.Int(), b.ReservedQty.Int())
	}
}

// TestReservation_CreateNoLevelsReturnsNotFound verifies ErrInventoryLevelNotFound
// when the item has no inventory_levels rows at all.
func TestReservation_CreateNoLevelsReturnsNotFound(t *testing.T) {
	repo := newFakeInventoryRepo()
	variantID := uuid.New()
	repo.addVariant(variantID, uuid.New())

	uc := NewReservationUseCase(repo)
	_, err := uc.Create(context.Background(), domain.CreateReservationInput{
		OrderID: uuid.New(),
		Items:   []domain.ReservationItemInput{{VariantID: variantID, Quantity: 1}},
	})
	if err != domain.ErrInventoryLevelNotFound {
		t.Fatalf("got %v, want ErrInventoryLevelNotFound", err)
	}
}
