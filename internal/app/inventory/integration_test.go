package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// integrationDB adapts a pgx pool to the database.Database interface so the
// inventory use cases can be exercised against a real PostgreSQL instance.
type integrationDB struct {
	pool *pgxpool.Pool
}

func (d *integrationDB) GetDb() *pgxpool.Pool { return d.pool }

func (d *integrationDB) WithTx(ctx context.Context, fn database.TxFunc) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *integrationDB) Close() { d.pool.Close() }

// connectIntegrationDB connects to DATABASE_URL and skips the test when the
// database is unreachable (e.g. `go test ./...` running offline).
func connectIntegrationDB(t *testing.T) database.Database {
	t.Helper()
	if err := loadEnvFromRepoRoot(); err != nil {
		t.Skipf("skip integration test: %v", err)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Skipf("skip integration test: parse db url: %v", err)
	}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("skip integration test: create pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skip integration test: cannot reach database: %v", err)
	}
	return &integrationDB{pool: pool}
}

// loadEnvFromRepoRoot loads .env from the module root so integration tests run
// correctly regardless of the package working directory.
func loadEnvFromRepoRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return godotenv.Load(filepath.Join(dir, ".env"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("could not locate module root (go.mod not found)")
		}
		dir = parent
	}
}

// inventoryFixture seeds an isolated product/variant/inventory-item/location
// graph and removes it after the test.
type inventoryFixture struct {
	db         database.Database
	pool       *pgxpool.Pool
	categoryID int
	productID  uuid.UUID
	variantID  uuid.UUID
	itemID     uuid.UUID
	locA       uuid.UUID
	locB       uuid.UUID
	orderIDs   []uuid.UUID
}

func newInventoryFixture(t *testing.T) *inventoryFixture {
	t.Helper()
	db := connectIntegrationDB(t)
	ctx := context.Background()
	f := &inventoryFixture{db: db, pool: db.GetDb()}

	unique := uuid.New().String()
	if err := db.GetDb().QueryRow(ctx,
		`INSERT INTO category (name, slug) VALUES ($1, $2) RETURNING id`,
		"itest-"+unique, "itest-"+unique).Scan(&f.categoryID); err != nil {
		t.Fatalf("insert category: %v", err)
	}
	if err := db.GetDb().QueryRow(ctx,
		`INSERT INTO products (handle, title, category_id, status) VALUES ($1, $2, $3, 'draft') RETURNING id`,
		"itest-"+unique, "itest product", f.categoryID).Scan(&f.productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	f.variantID = uuid.Must(uuid.NewV7())
	if _, err := db.GetDb().Exec(ctx,
		`INSERT INTO variants (id, product_id, title, price, weight, options) VALUES ($1, $2, $3, 0, 0, '[]')`,
		f.variantID, f.productID, "itest variant"); err != nil {
		t.Fatalf("insert variant: %v", err)
	}

	f.itemID = uuid.Must(uuid.NewV7())
	if _, err := db.GetDb().Exec(ctx,
		`INSERT INTO inventory_items (id, variant_id, track_inventory) VALUES ($1, $2, true)`,
		f.itemID, f.variantID); err != nil {
		t.Fatalf("insert inventory item: %v", err)
	}

	f.locA = uuid.Must(uuid.NewV7())
	f.locB = uuid.Must(uuid.NewV7())
	if _, err := db.GetDb().Exec(ctx,
		`INSERT INTO locations (id, name) VALUES ($1, $2), ($3, $4)`,
		f.locA, "itest-A", f.locB, "itest-B"); err != nil {
		t.Fatalf("insert locations: %v", err)
	}

	t.Cleanup(f.cleanup)
	return f
}

// useOrder registers order ids for cleanup (idempotency_keys + reservations).
func (f *inventoryFixture) useOrder(ids ...uuid.UUID) {
	f.orderIDs = append(f.orderIDs, ids...)
}

// setLevel sets the inventory level for the fixture's item at a location,
// upserting so tests can re-run against a dirty database.
func (f *inventoryFixture) setLevel(t *testing.T, locationID uuid.UUID, available, reserved int) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (inventory_item_id, location_id) DO UPDATE
		SET available_qty = EXCLUDED.available_qty, reserved_qty = EXCLUDED.reserved_qty`,
		uuid.Must(uuid.NewV7()), f.itemID, locationID, available, reserved); err != nil {
		t.Fatalf("set level: %v", err)
	}
}

func (f *inventoryFixture) getLevel(t *testing.T, locationID uuid.UUID) (available, reserved int) {
	t.Helper()
	err := f.pool.QueryRow(context.Background(), `
		SELECT COALESCE(available_qty, 0), COALESCE(reserved_qty, 0)
		FROM inventory_levels
		WHERE inventory_item_id = $1 AND location_id = $2`,
		f.itemID, locationID).Scan(&available, &reserved)
	if err != nil {
		t.Fatalf("get level: %v", err)
	}
	return
}

func (f *inventoryFixture) cleanup() {
	ctx := context.Background()
	_, _ = f.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE order_id = ANY($1)`, f.orderIDs)
	_, _ = f.pool.Exec(ctx, `DELETE FROM reservations WHERE inventory_item_id = $1`, f.itemID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM stock_moves WHERE inventory_item_id = $1`, f.itemID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM inventory_levels WHERE inventory_item_id = $1`, f.itemID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM inventory_items WHERE id = $1`, f.itemID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM variants WHERE id = $1`, f.variantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM products WHERE id = $1`, f.productID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM category WHERE id = $1`, f.categoryID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM locations WHERE id IN ($1, $2)`, f.locA, f.locB)
}

func newIntegrationRepo(f *inventoryFixture) domain.InventoryRepository {
	return repository.NewInventoryRepo(f.db)
}

func TestIntegration_StockMoves(t *testing.T) {
	f := newInventoryFixture(t)
	f.setLevel(t, f.locA, 10, 0)
	f.setLevel(t, f.locB, 5, 0)

	uc := NewStockMoveUseCase(newIntegrationRepo(f))
	ctx := context.Background()

	// IN 5 at A -> A=15
	if _, err := uc.Create(ctx, domain.CreateStockMoveInput{
		VariantID: f.variantID, MoveType: domain.StockMoveIn, Quantity: 5, ToLocationID: &f.locA,
	}); err != nil {
		t.Fatalf("IN: %v", err)
	}
	// OUT 5 from A -> A=10
	if _, err := uc.Create(ctx, domain.CreateStockMoveInput{
		VariantID: f.variantID, MoveType: domain.StockMoveOut, Quantity: 5, FromLocationID: &f.locA,
	}); err != nil {
		t.Fatalf("OUT: %v", err)
	}
	// TRANSFER 3 A->B -> A=7, B=8
	if _, err := uc.Create(ctx, domain.CreateStockMoveInput{
		VariantID: f.variantID, MoveType: domain.StockMoveTransfer, Quantity: 3,
		FromLocationID: &f.locA, ToLocationID: &f.locB,
	}); err != nil {
		t.Fatalf("TRANSFER: %v", err)
	}
	// ADJUST A to 20
	if _, err := uc.Create(ctx, domain.CreateStockMoveInput{
		VariantID: f.variantID, MoveType: domain.StockMoveAdjust, Quantity: 20, ToLocationID: &f.locA,
	}); err != nil {
		t.Fatalf("ADJUST: %v", err)
	}

	if a, _ := f.getLevel(t, f.locA); a != 20 {
		t.Fatalf("A available = %d, want 20", a)
	}
	if a, _ := f.getLevel(t, f.locB); a != 8 {
		t.Fatalf("B available = %d, want 8", a)
	}
}

func TestIntegration_ReservationLifecycle(t *testing.T) {
	f := newInventoryFixture(t)
	f.setLevel(t, f.locA, 10, 0)
	f.setLevel(t, f.locB, 5, 0)

	uc := NewReservationUseCase(newIntegrationRepo(f))
	ctx := context.Background()

	order1 := uuid.New()
	order2 := uuid.New()
	f.useOrder(order1, order2)

	// Reserve order1: 3 at A + 2 at B.
	res, err := uc.Create(ctx, domain.CreateReservationInput{
		OrderID: order1,
		Items: []domain.ReservationItemInput{
			{VariantID: f.variantID, LocationID: f.locA, Quantity: 3},
			{VariantID: f.variantID, LocationID: f.locB, Quantity: 2},
		},
	})
	if err != nil {
		t.Fatalf("reserve order1: %v", err)
	}
	if len(res.Reservations) != 2 {
		t.Fatalf("order1 reservations = %d, want 2", len(res.Reservations))
	}
	if a, r := f.getLevel(t, f.locA); a != 7 || r != 3 {
		t.Fatalf("A = %d/%d, want 7/3", a, r)
	}
	if a, r := f.getLevel(t, f.locB); a != 3 || r != 2 {
		t.Fatalf("B = %d/%d, want 3/2", a, r)
	}

	// Complete order1 (consumes reserved stock; available untouched).
	res, err = uc.Complete(ctx, order1)
	if err != nil {
		t.Fatalf("complete order1: %v", err)
	}
	if res.Status != domain.ReservationCompleted {
		t.Fatalf("complete status = %s", res.Status)
	}
	if a, r := f.getLevel(t, f.locA); a != 7 || r != 0 {
		t.Fatalf("after complete A = %d/%d, want 7/0", a, r)
	}
	// Double-complete is a no-op.
	if _, err := uc.Complete(ctx, order1); err != nil {
		t.Fatalf("double complete: %v", err)
	}
	if a, r := f.getLevel(t, f.locA); a != 7 || r != 0 {
		t.Fatalf("after double complete A = %d/%d, want 7/0", a, r)
	}

	// Reserve order2: 2 at A, then cancel (returns stock).
	if _, err := uc.Create(ctx, domain.CreateReservationInput{
		OrderID: order2,
		Items:   []domain.ReservationItemInput{{VariantID: f.variantID, LocationID: f.locA, Quantity: 2}},
	}); err != nil {
		t.Fatalf("reserve order2: %v", err)
	}
	if a, r := f.getLevel(t, f.locA); a != 5 || r != 2 {
		t.Fatalf("before cancel A = %d/%d, want 5/2", a, r)
	}
	res, err = uc.Cancel(ctx, order2)
	if err != nil {
		t.Fatalf("cancel order2: %v", err)
	}
	if res.Status != domain.ReservationCancelled {
		t.Fatalf("cancel status = %s", res.Status)
	}
	if a, r := f.getLevel(t, f.locA); a != 7 || r != 0 {
		t.Fatalf("after cancel A = %d/%d, want 7/0", a, r)
	}
	// Double-cancel is a no-op.
	if _, err := uc.Cancel(ctx, order2); err != nil {
		t.Fatalf("double cancel: %v", err)
	}
	if a, r := f.getLevel(t, f.locA); a != 7 || r != 0 {
		t.Fatalf("after double cancel A = %d/%d, want 7/0", a, r)
	}
}

func TestIntegration_ReservationOneItemInsufficientRollsBack(t *testing.T) {
	f := newInventoryFixture(t)
	f.setLevel(t, f.locA, 10, 0)
	f.setLevel(t, f.locB, 1, 0)

	uc := NewReservationUseCase(newIntegrationRepo(f))
	ctx := context.Background()
	order := uuid.New()
	f.useOrder(order)

	_, err := uc.Create(ctx, domain.CreateReservationInput{
		OrderID: order,
		Items: []domain.ReservationItemInput{
			{VariantID: f.variantID, LocationID: f.locA, Quantity: 3},
			{VariantID: f.variantID, LocationID: f.locB, Quantity: 2}, // B only has 1
		},
	})
	if err != domain.ErrInsufficientStock {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}

	if a, r := f.getLevel(t, f.locA); a != 10 || r != 0 {
		t.Fatalf("A must be untouched: %d/%d", a, r)
	}
	if a, r := f.getLevel(t, f.locB); a != 1 || r != 0 {
		t.Fatalf("B must be untouched: %d/%d", a, r)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM reservations WHERE order_id = $1`, order).Scan(&count); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no reservation rows, got %d", count)
	}
}

func TestIntegration_ReservationIdempotentRetry(t *testing.T) {
	f := newInventoryFixture(t)
	f.setLevel(t, f.locA, 10, 0)

	uc := NewReservationUseCase(newIntegrationRepo(f))
	ctx := context.Background()
	order := uuid.New()
	f.useOrder(order)

	input := domain.CreateReservationInput{
		OrderID: order,
		Items:   []domain.ReservationItemInput{{VariantID: f.variantID, LocationID: f.locA, Quantity: 3}},
	}

	first, err := uc.Create(ctx, input)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := uc.Create(ctx, input)
	if err != nil {
		t.Fatalf("retry create: %v", err)
	}
	if len(second.Reservations) != len(first.Reservations) {
		t.Fatalf("retry returned %d reservations, want %d", len(second.Reservations), len(first.Reservations))
	}
	if second.Reservations[0].ID != first.Reservations[0].ID {
		t.Fatalf("retry must return the existing reservation id")
	}
	if a, r := f.getLevel(t, f.locA); a != 7 || r != 3 {
		t.Fatalf("after retry A = %d/%d, want 7/3 (no double reserve)", a, r)
	}
}

// TestIntegration_ConcurrentReservation exercises the spec Section 28 race:
// available = 10, two concurrent requests reserve 7 each. Exactly one must
// succeed and the final available_qty must never go negative.
func TestIntegration_ConcurrentReservation(t *testing.T) {
	f := newInventoryFixture(t)
	f.setLevel(t, f.locA, 10, 0)

	uc := NewReservationUseCase(newIntegrationRepo(f))
	ctx := context.Background()

	orderA, orderB := uuid.New(), uuid.New()
	f.useOrder(orderA, orderB)

	type outcome struct {
		err error
	}
	results := make([]outcome, 2)
	var wg sync.WaitGroup
	for i, order := range []uuid.UUID{orderA, orderB} {
		wg.Add(1)
		go func(i int, order uuid.UUID) {
			defer wg.Done()
			_, err := uc.Create(ctx, domain.CreateReservationInput{
				OrderID: order,
				Items:   []domain.ReservationItemInput{{VariantID: f.variantID, LocationID: f.locA, Quantity: 7}},
			})
			results[i].err = err
		}(i, order)
	}
	wg.Wait()

	successes := 0
	for _, r := range results {
		if r.err == nil {
			successes++
		} else if !errors.Is(r.err, domain.ErrInsufficientStock) {
			t.Fatalf("unexpected error: %v", r.err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 success, got %d (results: %+v)", successes, results)
	}

	a, r := f.getLevel(t, f.locA)
	if a != 3 || r != 7 {
		t.Fatalf("final A = %d/%d, want 3/7", a, r)
	}
	if a < 0 || r < 0 {
		t.Fatalf("quantities must never be negative: %d/%d", a, r)
	}
}
