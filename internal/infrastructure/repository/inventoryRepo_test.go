package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/repository/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
		"inventory_levels_available_nonnegative",
		"inventory_levels_reserved_nonnegative",
		"locations_single_default",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration missing invariant %q", want)
		}
	}
	if strings.Contains(sql, "ALTER TABLE stock_moves") {
		t.Error("checkout migration must preserve the existing stock_moves schema")
	}
	if strings.Contains(sql, "reservation_id") {
		t.Error("checkout migration must not reference a reservation_id column on stock_moves")
	}
}

// canonicalStockMoveColumns is the authoritative stock_moves shape: a pure
// quantity and location audit trail. Reservation identity lives on the
// reservations table and must never be duplicated onto a movement.
var canonicalStockMoveColumns = []string{
	"id",
	"inventory_item_id",
	"from_location_id",
	"to_location_id",
	"move_type",
	"quantity",
	"created_by",
	"reason",
	"created_at",
}

// TestStockMovesSchemaHasNoReservationID guards the schema source of truth: the
// stock_moves table keeps exactly its nine canonical columns, and neither the
// diagram nor any migration introduces a reservation_id column on stock_moves.
func TestStockMovesSchemaHasNoReservationID(t *testing.T) {
	dbml, err := os.ReadFile("../../../db.dbml")
	if err != nil {
		t.Fatalf("read db.dbml: %v", err)
	}
	body := dbmlTableBody(t, string(dbml), "stock_moves")

	if got := dbmlColumnNames(body); !slices.Equal(got, canonicalStockMoveColumns) {
		t.Errorf("stock_moves columns = %v, want %v", got, canonicalStockMoveColumns)
	}
	if strings.Contains(body, "reservation_id") {
		t.Error("stock_moves must not define a reservation_id column")
	}

	diagram, err := os.ReadFile("../../../db.dbdiagram")
	if err != nil {
		t.Fatalf("read db.dbdiagram: %v", err)
	}
	if strings.Contains(string(diagram), "reservation_id") {
		t.Error("db.dbdiagram must not reference stock_moves.reservation_id")
	}

	migrations, err := filepath.Glob("../../../migrations/*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations found to verify")
	}
	for _, path := range migrations {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(sql), "reservation_id") {
			t.Errorf("%s must not reference a reservation_id column on stock_moves", filepath.Base(path))
		}
	}
}

// dbmlTableBody returns the body of the `Table <name> { ... }` declaration in a
// DBML source, failing the test when the table is missing or unclosed.
func dbmlTableBody(t *testing.T, source, name string) string {
	t.Helper()
	header := "Table " + name + " {"
	start := strings.Index(source, header)
	if start < 0 {
		t.Fatalf("db.dbml does not define table %q", name)
	}
	rest := source[start+len(header):]
	end := strings.Index(rest, "}")
	if end < 0 {
		t.Fatalf("db.dbml table %q is not closed", name)
	}
	return rest[:end]
}

// dbmlColumnNames lists the column names declared in a DBML table body in
// declaration order, ignoring blank lines, comments, indexes and notes.
func dbmlColumnNames(body string) []string {
	var cols []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if strings.EqualFold(fields[0], "indexes") || strings.EqualFold(fields[0], "note") {
			continue
		}
		cols = append(cols, fields[0])
	}
	return cols
}

// TestMigration0006DropsOnlyParentRequest is an isolated, database-free check
// that the post-rollout migration removes only the parent-request table and
// leaves every durable reservation artifact untouched.
func TestMigration0006DropsOnlyParentRequest(t *testing.T) {
	data, err := os.ReadFile("../../../migrations/0006_order_owned_reservation_expiry.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)

	if !strings.Contains(sql, "DROP TABLE IF EXISTS checkout_reservation_requests") {
		t.Error("migration must drop checkout_reservation_requests")
	}
	for _, forbidden := range []string{
		"DELETE FROM reservations",
		"UPDATE reservations",
		"DELETE FROM stock_moves",
		"UPDATE stock_moves",
		"DELETE FROM inventory_levels",
		"UPDATE inventory_levels",
		"CREATE TABLE checkout_reservation_requests",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("migration must not touch durable reservation data, found %q", forbidden)
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
	locationID, variantID, itemID := seedInventoryFixture(ctx, t, pool, 5)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	input := domain.CreateReservationInput{
		OrderID:   uuid.New(),
		ExpiresAt: expiresAt,
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
		t.Fatal("expiry must be set")
	}
	// The caller-owned expiry must be persisted and returned unchanged.
	if !item.ExpiresAt.Equal(expiresAt) {
		t.Errorf("expiry = %v, want %v", item.ExpiresAt, expiresAt)
	}

	var available, reserved int
	if err := pool.QueryRow(ctx, `SELECT available_qty, reserved_qty FROM inventory_levels WHERE inventory_item_id = $1`, itemID).Scan(&available, &reserved); err != nil {
		t.Fatalf("read level: %v", err)
	}
	if available != 3 || reserved != 2 {
		t.Errorf("level = %d/%d, want 3/2", available, reserved)
	}

	rows, err := pool.Query(ctx, `
		SELECT id, inventory_item_id, from_location_id, to_location_id,
		       move_type, quantity, created_by, reason, created_at
		FROM stock_moves WHERE id = $1`, input.Items[0].StockMoveID)
	if err != nil {
		t.Fatalf("read move: %v", err)
	}
	move, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[model.StockMove])
	if err != nil {
		t.Fatalf("map move using existing schema: %v", err)
	}
	if move.ID != input.Items[0].StockMoveID || move.InventoryItemID != itemID ||
		move.FromLocationID == nil || *move.FromLocationID != locationID ||
		move.ToLocationID != nil || move.MoveType != "RESERVE" || move.Quantity != 2 {
		t.Errorf("move mismatch: %+v", move)
	}
	if move.CreatedBy != nil || move.Reason != nil || move.CreatedAt.IsZero() {
		t.Errorf("unexpected move defaults: %+v", move)
	}
}

func TestInventoryRepo_CreateReservation_RollbackOnInsufficient(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, itemID := seedInventoryFixture(ctx, t, pool, 1)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	input := domain.CreateReservationInput{
		OrderID:   uuid.New(),
		ExpiresAt: time.Now().Add(time.Hour).UTC(),
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

	var reservations, moves int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reservations`).Scan(&reservations); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM stock_moves WHERE move_type = 'RESERVE'`).Scan(&moves); err != nil {
		t.Fatalf("count moves: %v", err)
	}
	if reservations != 0 || moves != 0 {
		t.Errorf("rollback must leave no partial writes: reservations=%d moves=%d", reservations, moves)
	}
}

func TestInventoryRepo_CreateReservation_IdempotentRetryAndConflict(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, _ := seedInventoryFixture(ctx, t, pool, 5)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	orderID := uuid.New()
	reservationID := uuid.New()
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	build := func(qty int, expiry time.Time) domain.CreateReservationInput {
		return domain.CreateReservationInput{
			OrderID:   orderID,
			ExpiresAt: expiry,
			Items: []domain.CreateReservationItem{
				{VariantID: variantID, Quantity: domain.Quantity(qty), ReservationID: reservationID, StockMoveID: uuid.New()},
			},
		}
	}

	first, err := repo.CreateReservation(ctx, build(2, expiresAt))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	retry, err := repo.CreateReservation(ctx, build(2, expiresAt))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !retry.Retried {
		t.Fatal("retry must be detected as persisted")
	}
	if retry.Items[0].ReservationID != first.Items[0].ReservationID {
		t.Errorf("retry returned different reservation IDs: %v vs %v", retry.Items[0].ReservationID, first.Items[0].ReservationID)
	}
	if !retry.Items[0].ExpiresAt.Equal(expiresAt) {
		t.Errorf("retry expiry = %v, want %v", retry.Items[0].ExpiresAt, expiresAt)
	}

	// Changed quantity must conflict without writes.
	if _, err := repo.CreateReservation(ctx, build(1, expiresAt)); err != domain.ErrReservationConflict {
		t.Fatalf("changed quantity got %v, want ErrReservationConflict", err)
	}

	// Changed expiry must conflict without writes.
	if _, err := repo.CreateReservation(ctx, build(2, expiresAt.Add(time.Hour))); err != domain.ErrReservationConflict {
		t.Fatalf("changed expiry got %v, want ErrReservationConflict", err)
	}

	// Only the original single reservation must remain.
	var reservations, moves int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reservations WHERE order_id = $1`, orderID).Scan(&reservations); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM stock_moves WHERE move_type = 'RESERVE'`).Scan(&moves); err != nil {
		t.Fatalf("count moves: %v", err)
	}
	if reservations != 1 || moves != 1 {
		t.Errorf("conflicting retries must not write: reservations=%d moves=%d, want 1/1", reservations, moves)
	}
}

func TestInventoryRepo_CreateReservation_EquivalentOffsetRetry(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, _ := seedInventoryFixture(ctx, t, pool, 3)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	orderID := uuid.New()
	base := time.Date(2030, 1, 1, 20, 4, 5, 123456000, time.UTC)
	build := func(expiry time.Time) domain.CreateReservationInput {
		return domain.CreateReservationInput{
			OrderID:   orderID,
			ExpiresAt: expiry,
			Items: []domain.CreateReservationItem{
				{VariantID: variantID, Quantity: 2, ReservationID: uuid.New(), StockMoveID: uuid.New()},
			},
		}
	}

	first, err := repo.CreateReservation(ctx, build(base))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// The same instant expressed with a different offset must retry.
	retry, err := repo.CreateReservation(ctx, build(base.In(time.FixedZone("+07", 7*3600))))
	if err != nil {
		t.Fatalf("equivalent-offset retry: %v", err)
	}
	if !retry.Retried {
		t.Fatal("equivalent-offset retry must be detected as persisted")
	}
	if retry.Items[0].ReservationID != first.Items[0].ReservationID {
		t.Errorf("retry returned different reservation IDs")
	}
}

func TestInventoryRepo_CreateReservation_HundredIdenticalRetries(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, itemID := seedInventoryFixture(ctx, t, pool, 3)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	orderID := uuid.New()
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	build := func() domain.CreateReservationInput {
		return domain.CreateReservationInput{
			OrderID:   orderID,
			ExpiresAt: expiresAt,
			Items: []domain.CreateReservationItem{
				{VariantID: variantID, Quantity: 2, ReservationID: uuid.New(), StockMoveID: uuid.New()},
			},
		}
	}

	var firstID uuid.UUID
	for i := 0; i < 100; i++ {
		res, err := repo.CreateReservation(ctx, build())
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if i == 0 {
			if res.Retried {
				t.Fatal("first request must create, not retry")
			}
			firstID = res.Items[0].ReservationID
		} else {
			if !res.Retried {
				t.Fatalf("retry %d must be detected as persisted", i)
			}
			if res.Items[0].ReservationID != firstID {
				t.Fatalf("retry %d returned a different reservation ID", i)
			}
		}
	}

	var reservations, moves int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reservations WHERE order_id = $1`, orderID).Scan(&reservations); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM stock_moves WHERE move_type = 'RESERVE'`).Scan(&moves); err != nil {
		t.Fatalf("count moves: %v", err)
	}
	if reservations != 1 || moves != 1 {
		t.Errorf("100 retries must produce one reservation and one movement: reservations=%d moves=%d", reservations, moves)
	}

	var available, reserved int
	if err := pool.QueryRow(ctx, `SELECT available_qty, reserved_qty FROM inventory_levels WHERE inventory_item_id = $1`, itemID).Scan(&available, &reserved); err != nil {
		t.Fatalf("read level: %v", err)
	}
	if available != 1 || reserved != 2 {
		t.Errorf("level = %d/%d, want 1/2", available, reserved)
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
				OrderID:   uuid.New(),
				ExpiresAt: time.Now().Add(time.Hour).UTC(),
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

// seedAdditionalVariant seeds a second tracked variant (with its own inventory
// item and level) at an existing default location, for multi-variant tests.
func seedAdditionalVariant(ctx context.Context, t *testing.T, pool *pgxpool.Pool, locationID uuid.UUID, available int) (uuid.UUID, uuid.UUID) {
	t.Helper()

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

	return variantID, itemID
}

func TestInventoryRepo_CreateReservation_ExpiredExpiryRejected(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	_, variantID, itemID := seedInventoryFixture(ctx, t, pool, 5)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	input := domain.CreateReservationInput{
		OrderID:   uuid.New(),
		ExpiresAt: time.Now().Add(-time.Hour).UTC(),
		Items: []domain.CreateReservationItem{
			{VariantID: variantID, Quantity: 1, ReservationID: uuid.New(), StockMoveID: uuid.New()},
		},
	}
	_, err := repo.CreateReservation(ctx, input)
	if err != domain.ErrExpiredExpiry {
		t.Fatalf("got %v, want ErrExpiredExpiry", err)
	}

	var available, reserved int
	if err := pool.QueryRow(ctx, `SELECT available_qty, reserved_qty FROM inventory_levels WHERE inventory_item_id = $1`, itemID).Scan(&available, &reserved); err != nil {
		t.Fatalf("read level: %v", err)
	}
	if available != 5 || reserved != 0 {
		t.Errorf("level must be unchanged after expired expiry: %d/%d, want 5/0", available, reserved)
	}

	var reservations, moves int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reservations`).Scan(&reservations); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM stock_moves WHERE move_type = 'RESERVE'`).Scan(&moves); err != nil {
		t.Fatalf("count moves: %v", err)
	}
	if reservations != 0 || moves != 0 {
		t.Errorf("expired expiry must write nothing: reservations=%d moves=%d", reservations, moves)
	}
}

func TestInventoryRepo_CreateReservation_ConcurrentDisjointFirstRequests(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	locationID, variantA, _ := seedInventoryFixture(ctx, t, pool, 10)
	variantB, _ := seedAdditionalVariant(ctx, t, pool, locationID, 10)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	orderID := uuid.New()
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)

	results := make(chan error, 2)
	for _, vid := range []uuid.UUID{variantA, variantB} {
		go func(v uuid.UUID) {
			input := domain.CreateReservationInput{
				OrderID:   orderID,
				ExpiresAt: expiresAt,
				Items: []domain.CreateReservationItem{
					{VariantID: v, Quantity: 1, ReservationID: uuid.New(), StockMoveID: uuid.New()},
				},
			}
			_, err := repo.CreateReservation(ctx, input)
			results <- err
		}(vid)
	}

	var success, conflict int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			success++
		case err == domain.ErrReservationConflict:
			conflict++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("want exactly one complete set (1 success, 1 conflict), got success=%d conflict=%d", success, conflict)
	}

	var reservations, moves int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reservations WHERE order_id = $1`, orderID).Scan(&reservations); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM stock_moves WHERE move_type = 'RESERVE'`).Scan(&moves); err != nil {
		t.Fatalf("count moves: %v", err)
	}
	if reservations != 1 || moves != 1 {
		t.Errorf("one order must produce exactly one complete set: reservations=%d moves=%d", reservations, moves)
	}
}

func TestInventoryRepo_CreateReservation_MultiItemOrderAssociation(t *testing.T) {
	pool := requireTestDB(t)
	ctx := context.Background()
	locationID, variantA, _ := seedInventoryFixture(ctx, t, pool, 10)
	variantB, _ := seedAdditionalVariant(ctx, t, pool, locationID, 10)

	repo := NewInventoryRepo(&testDatabase{pool: pool})

	orderID := uuid.New()
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	input := domain.CreateReservationInput{
		OrderID:   orderID,
		ExpiresAt: expiresAt,
		Items: []domain.CreateReservationItem{
			{VariantID: variantA, Quantity: 2, ReservationID: uuid.New(), StockMoveID: uuid.New()},
			{VariantID: variantB, Quantity: 3, ReservationID: uuid.New(), StockMoveID: uuid.New()},
		},
	}

	result, err := repo.CreateReservation(ctx, input)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	for _, it := range result.Items {
		if !it.ExpiresAt.Equal(expiresAt) {
			t.Errorf("item %v expiry = %v, want %v", it.VariantID, it.ExpiresAt, expiresAt)
		}
	}

	// Every row must retain the order id and the caller-owned expiry, and the
	// rows alone must drive retry lookup.
	rows, err := pool.Query(ctx, `
		SELECT order_id, expires_at
		FROM reservations
		WHERE order_id = $1
		ORDER BY inventory_item_id`, orderID)
	if err != nil {
		t.Fatalf("query reservations: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var gotOrder uuid.UUID
		var gotExpiry time.Time
		if err := rows.Scan(&gotOrder, &gotExpiry); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if gotOrder != orderID {
			t.Errorf("reservation order_id = %v, want %v", gotOrder, orderID)
		}
		if !gotExpiry.Equal(expiresAt) {
			t.Errorf("reservation expiry = %v, want %v", gotExpiry, expiresAt)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("persisted rows = %d, want 2", count)
	}
}
