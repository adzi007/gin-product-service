package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testDatabase adapts a raw pool to the repository's database.Database contract
// for integration tests.
type testDatabase struct {
	pool *pgxpool.Pool
}

func (d *testDatabase) GetDb() *pgxpool.Pool { return d.pool }
func (d *testDatabase) Close()               { d.pool.Close() }

// requireTestDB returns a connected pool or skips when TEST_DATABASE_URL is
// unset, so `go test ./...` stays green without a disposable database.
func requireTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestMigration0005ContainsRequiredInvariants is an isolated, database-free
// coverage of the forward-only migration: it asserts the file defines every
// integrity invariant the feature depends on.
func TestMigration0005ContainsRequiredInvariants(t *testing.T) {
	data, err := os.ReadFile("../../../migrations/0005_checkout_reservation_integrity.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)

	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS checkout_reservation_requests",
		"request_fingerprint",
		"reservations_quantity_positive",
		"reservations_expiry_after_reserved",
		"reservations_order_item_unique",
		"ADD COLUMN IF NOT EXISTS reservation_id",
		"stock_moves_reservation_id_fk",
		"stock_moves_reservation_id_unique",
		"inventory_levels_available_nonnegative",
		"inventory_levels_reserved_nonnegative",
		"locations_single_default",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration missing invariant %q", want)
		}
	}
}

// seedInventoryFixture seeds a default location plus one tracked variant with a
// level at that location, and returns the location, variant, and item IDs.
func seedInventoryFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool, available int) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()

	locationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO locations (id, name, type, is_default) VALUES ($1, 'default', 'warehouse', true)`, locationID); err != nil {
		t.Fatalf("insert location: %v", err)
	}

	productID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO products (id, handle, title, category_id, status) VALUES ($1, $2, $3, $4, $5)`,
		productID, "seed-"+productID.String()[:8], "Seed", 1, "active"); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	variantID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO variants (id, product_id, sku, title, price) VALUES ($1, $2, $3, $4, 10)`,
		variantID, productID, "SKU-"+variantID.String()[:8], "Seed variant"); err != nil {
		t.Fatalf("insert variant: %v", err)
	}

	itemID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO inventory_items (id, variant_id, track_inventory) VALUES ($1, $2, true)`,
		itemID, variantID); err != nil {
		t.Fatalf("insert inventory item: %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty) VALUES ($1, $2, $3, $4, 0)`,
		uuid.New(), itemID, locationID, available); err != nil {
		t.Fatalf("insert level: %v", err)
	}

	return locationID, variantID, itemID
}

func TestInventoryRepo_CreateReservation_HappyPath(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, itemID := seedInventoryFixture(ctx, t, pool, 5)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	input := domain.CreateReservationInput{
		OrderID: uuid.New(),
		Items: []domain.CreateReservationItem{
			{VariantID: variantID, Quantity: 2, ReservationID: uuid.New(), StockMoveID: uuid.New()},
		},
	}
	result, err := repo.CreateReservation(ctx, input)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Status != domain.ReservationActive || item.Quantity != 2 || item.VariantID != variantID {
		t.Errorf("item mismatch: %+v", item)
	}
	if item.ExpiresAt.IsZero() {
		t.Fatal("expiry must be set by the database")
	}

	var available, reserved int
	if err := pool.QueryRow(ctx, `SELECT available_qty, reserved_qty FROM inventory_levels WHERE inventory_item_id = $1`, itemID).Scan(&available, &reserved); err != nil {
		t.Fatalf("read level: %v", err)
	}
	if available != 3 || reserved != 2 {
		t.Errorf("level = %d/%d, want 3/2", available, reserved)
	}

	var moveCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM stock_moves WHERE reservation_id = $1`, item.ReservationID).Scan(&moveCount); err != nil {
		t.Fatalf("count moves: %v", err)
	}
	if moveCount != 1 {
		t.Errorf("move count = %d, want 1", moveCount)
	}
}

func TestInventoryRepo_CreateReservation_RollbackOnInsufficient(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, itemID := seedInventoryFixture(ctx, t, pool, 1)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	input := domain.CreateReservationInput{
		OrderID: uuid.New(),
		Items: []domain.CreateReservationItem{
			{VariantID: variantID, Quantity: 2, ReservationID: uuid.New(), StockMoveID: uuid.New()},
		},
	}
	_, err := repo.CreateReservation(ctx, input)
	var ve *domain.VariantError
	if !errors.As(err, &ve) || ve.Err != domain.ErrInsufficientStock {
		t.Fatalf("got %v, want insufficient-stock VariantError", err)
	}

	var available, reserved int
	if err := pool.QueryRow(ctx, `SELECT available_qty, reserved_qty FROM inventory_levels WHERE inventory_item_id = $1`, itemID).Scan(&available, &reserved); err != nil {
		t.Fatalf("read level: %v", err)
	}
	if available != 1 || reserved != 0 {
		t.Errorf("level must be unchanged after rollback: %d/%d, want 1/0", available, reserved)
	}

	var reservations, moves, claims int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reservations`).Scan(&reservations); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM stock_moves WHERE move_type = 'RESERVE'`).Scan(&moves); err != nil {
		t.Fatalf("count moves: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM checkout_reservation_requests`).Scan(&claims); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if reservations != 0 || moves != 0 || claims != 0 {
		t.Errorf("rollback must leave no partial writes: reservations=%d moves=%d claims=%d", reservations, moves, claims)
	}
}

func TestInventoryRepo_CreateReservation_IdempotentRetryAndConflict(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, _ := seedInventoryFixture(ctx, t, pool, 5)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	orderID := uuid.New()
	reservationID := uuid.New()
	build := func(qty int) domain.CreateReservationInput {
		return domain.CreateReservationInput{
			OrderID: orderID,
			Items: []domain.CreateReservationItem{
				{VariantID: variantID, Quantity: domain.Quantity(qty), ReservationID: reservationID, StockMoveID: uuid.New()},
			},
		}
	}

	first, err := repo.CreateReservation(ctx, build(2))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	retry, err := repo.CreateReservation(ctx, build(2))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !retry.Retried {
		t.Fatal("retry must be detected as persisted")
	}
	if retry.Items[0].ReservationID != first.Items[0].ReservationID {
		t.Errorf("retry returned different reservation IDs: %v vs %v", retry.Items[0].ReservationID, first.Items[0].ReservationID)
	}

	if _, err := repo.CreateReservation(ctx, build(1)); err != domain.ErrReservationConflict {
		t.Fatalf("changed retry got %v, want ErrReservationConflict", err)
	}
}

func TestInventoryRepo_CreateReservation_ConcurrentNoOverReserve(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, itemID := seedInventoryFixture(ctx, t, pool, 10)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	const requests = 100
	var wg sync.WaitGroup
	successes := make(chan bool, requests)

	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			input := domain.CreateReservationInput{
				OrderID: uuid.New(),
				Items: []domain.CreateReservationItem{
					{VariantID: variantID, Quantity: 1, ReservationID: uuid.New(), StockMoveID: uuid.New()},
				},
			}
			_, err := repo.CreateReservation(ctx, input)
			successes <- err == nil
		}()
	}
	wg.Wait()
	close(successes)

	var ok int
	for s := range successes {
		if s {
			ok++
		}
	}
	if ok > 10 {
		t.Fatalf("successful reservations = %d, must not exceed initial availability 10", ok)
	}

	var available, reserved int
	if err := pool.QueryRow(ctx, `SELECT available_qty, reserved_qty FROM inventory_levels WHERE inventory_item_id = $1`, itemID).Scan(&available, &reserved); err != nil {
		t.Fatalf("read level: %v", err)
	}
	if available < 0 || reserved < 0 || available+reserved != 10 {
		t.Errorf("invariant violated: available=%d reserved=%d (must be non-negative, total 10)", available, reserved)
	}
	if reserved != ok {
		t.Errorf("reserved=%d, want %d (successful holds)", reserved, ok)
	}
}
