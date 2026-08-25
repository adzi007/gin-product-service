package inventory

import (
	"context"
	"sort"
	"sync"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// levelKey identifies an inventory level by item + location.
type levelKey struct {
	itemID     uuid.UUID
	locationID uuid.UUID
}

// fakeInventoryRepo is an in-memory implementation of domain.InventoryRepository
// used to unit-test the inventory use cases. WithTx simply runs fn; the
// transaction handle is ignored because every other method operates on the
// fake's own state. Levels are returned by value (copies) and only persisted
// via UpdateInventoryLevel, mirroring real lock-then-update semantics.
type fakeInventoryRepo struct {
	mu sync.Mutex

	itemByVariant map[uuid.UUID]uuid.UUID
	locations     map[uuid.UUID]bool
	// defaultLocations tracks which locations have is_default = true, used by
	// LockInventoryLevelByItem's selection rule (spec:
	// remove-location-from-reservation.md).
	defaultLocations map[uuid.UUID]bool
	levels           map[levelKey]domain.InventoryLevel
	moves            []domain.StockMove
	reservations     []domain.Reservation

	// idempotencyKeys mirrors the idempotency_keys table: order_id -> claimed.
	idempotencyKeys map[uuid.UUID]bool

	// injected errors
	resolveErr       error
	lockErr          error
	insertReserveErr error
}

func newFakeInventoryRepo() *fakeInventoryRepo {
	return &fakeInventoryRepo{
		itemByVariant:    map[uuid.UUID]uuid.UUID{},
		locations:        map[uuid.UUID]bool{},
		defaultLocations: map[uuid.UUID]bool{},
		levels:           map[levelKey]domain.InventoryLevel{},
		idempotencyKeys:  map[uuid.UUID]bool{},
	}
}

func (f *fakeInventoryRepo) addVariant(variantID, itemID uuid.UUID) {
	f.itemByVariant[variantID] = itemID
}

func (f *fakeInventoryRepo) addLocation(id uuid.UUID) {
	f.locations[id] = true
}

func (f *fakeInventoryRepo) addDefaultLocation(id uuid.UUID) {
	f.locations[id] = true
	f.defaultLocations[id] = true
}

func (f *fakeInventoryRepo) addLevel(itemID, locationID uuid.UUID, available, reserved int) {
	f.levels[levelKey{itemID, locationID}] = domain.InventoryLevel{
		ID:              uuid.Must(uuid.NewV7()),
		InventoryItemID: itemID,
		LocationID:      locationID,
		AvailableQty:    domain.Quantity(available),
		ReservedQty:     domain.Quantity(reserved),
		UpdatedAt:       time.Now().UTC(),
	}
}

func (f *fakeInventoryRepo) level(itemID, locationID uuid.UUID) (domain.InventoryLevel, bool) {
	l, ok := f.levels[levelKey{itemID, locationID}]
	return l, ok
}

func (f *fakeInventoryRepo) WithTx(ctx context.Context, fn func(ctx context.Context, tx domain.Tx) error) error {
	return fn(ctx, nil)
}

func (f *fakeInventoryRepo) ResolveInventoryItemIDByVariant(ctx context.Context, tx domain.Tx, variantID uuid.UUID) (uuid.UUID, error) {
	if f.resolveErr != nil {
		return uuid.Nil, f.resolveErr
	}
	itemID, ok := f.itemByVariant[variantID]
	if !ok {
		return uuid.Nil, domain.ErrVariantNotFound
	}
	return itemID, nil
}

func (f *fakeInventoryRepo) LocationExists(ctx context.Context, tx domain.Tx, locationID uuid.UUID) (bool, error) {
	return f.locations[locationID], nil
}

func (f *fakeInventoryRepo) LockInventoryLevel(ctx context.Context, tx domain.Tx, itemID, locationID uuid.UUID) (domain.InventoryLevel, error) {
	if f.lockErr != nil {
		return domain.InventoryLevel{}, f.lockErr
	}
	l, ok := f.levels[levelKey{itemID, locationID}]
	if !ok {
		return domain.InventoryLevel{}, domain.ErrInventoryLevelNotFound
	}
	return l, nil
}

// LockInventoryLevelByItem mimics the repository selection rule: prefer the
// is_default location with enough available_qty, else the highest
// available_qty, tie-broken by location_id ASC. Returns
// ErrInventoryLevelNotFound when the item has no levels.
func (f *fakeInventoryRepo) LockInventoryLevelByItem(ctx context.Context, tx domain.Tx, itemID uuid.UUID, requiredQty domain.Quantity) (domain.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var candidates []domain.InventoryLevel
	for key, lvl := range f.levels {
		if key.itemID == itemID {
			candidates = append(candidates, lvl)
		}
	}
	if len(candidates) == 0 {
		return domain.InventoryLevel{}, domain.ErrInventoryLevelNotFound
	}

	sort.Slice(candidates, func(i, j int) bool {
		iDefault := f.defaultLocations[candidates[i].LocationID] && candidates[i].AvailableQty >= requiredQty
		jDefault := f.defaultLocations[candidates[j].LocationID] && candidates[j].AvailableQty >= requiredQty
		if iDefault != jDefault {
			return iDefault
		}
		if candidates[i].AvailableQty != candidates[j].AvailableQty {
			return candidates[i].AvailableQty > candidates[j].AvailableQty
		}
		return candidates[i].LocationID.String() < candidates[j].LocationID.String()
	})
	return candidates[0], nil
}

func (f *fakeInventoryRepo) UpdateInventoryLevel(ctx context.Context, tx domain.Tx, level domain.InventoryLevel) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.levels[levelKey{level.InventoryItemID, level.LocationID}] = level
	return nil
}

func (f *fakeInventoryRepo) InsertStockMove(ctx context.Context, tx domain.Tx, move domain.StockMove) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moves = append(f.moves, move)
	return nil
}

func (f *fakeInventoryRepo) InsertReservation(ctx context.Context, tx domain.Tx, r domain.Reservation) error {
	if f.insertReserveErr != nil {
		return f.insertReserveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reservations = append(f.reservations, r)
	return nil
}

func (f *fakeInventoryRepo) TryAcquireIdempotencyKey(ctx context.Context, tx domain.Tx, orderID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idempotencyKeys[orderID] {
		return false, nil
	}
	f.idempotencyKeys[orderID] = true
	return true, nil
}

func (f *fakeInventoryRepo) FindActiveReservationsByOrderID(ctx context.Context, tx domain.Tx, orderID uuid.UUID) ([]domain.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Reservation
	for _, r := range f.reservations {
		if r.OrderID != nil && *r.OrderID == orderID && r.Status == domain.ReservationActive {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeInventoryRepo) FindReservationsByOrderID(ctx context.Context, tx domain.Tx, orderID uuid.UUID) ([]domain.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Reservation
	for _, r := range f.reservations {
		if r.OrderID != nil && *r.OrderID == orderID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeInventoryRepo) UpdateReservationStatus(ctx context.Context, tx domain.Tx, reservationID uuid.UUID, status domain.ReservationStatus, releasedAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.reservations {
		if f.reservations[i].ID == reservationID {
			f.reservations[i].Status = status
			f.reservations[i].ReleasedAt = releasedAt
			return nil
		}
	}
	return domain.ErrReservationNotFound
}

func (f *fakeInventoryRepo) FindDueActiveReservations(ctx context.Context, tx domain.Tx, now time.Time) ([]domain.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Reservation
	for _, r := range f.reservations {
		if r.Status == domain.ReservationActive && r.ExpiresAt != nil && r.ExpiresAt.Before(now) {
			out = append(out, r)
		}
	}
	return out, nil
}
