package repository

import (
	"context"
	"errors"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/metrics"
	"gin-product-service/internal/infrastructure/repository/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type inventoryRepo struct {
	db database.Database
}

func NewInventoryRepo(db database.Database) domain.InventoryRepository {
	return &inventoryRepo{db: db}
}

var (
	_ domain.InventoryRepository = (*inventoryRepo)(nil)
)

// reservationSelect is the shared projection for reservation reads. It joins
// inventory_items so the cross-service variant_id is available, and coalesces
// the nullable columns that have DB defaults.
const reservationSelect = `
	SELECT
		r.id,
		r.inventory_item_id,
		ii.variant_id,
		r.location_id,
		r.order_id,
		r.quantity,
		COALESCE(r.reserved_at, now()) AS reserved_at,
		r.expires_at,
		r.status,
		r.released_at
	FROM reservations r
	JOIN inventory_items ii ON ii.id = r.inventory_item_id
`

// WithTx runs fn inside a single database transaction (spec Section 3.2).
func (r *inventoryRepo) WithTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return r.db.WithTx(ctx, fn)
}

// ResolveInventoryItemIDByVariant returns the inventory_item.id for a variant,
// verifying the variant exists first (spec Sections 3.1 and 24). Returns
// ErrVariantNotFound when the variant is missing/deleted and
// ErrInventoryItemNotFound when no inventory item has been created for it.
func (r *inventoryRepo) ResolveInventoryItemIDByVariant(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (uuid.UUID, error) {
	defer metrics.ObserveDB("inventory", "resolve_inventory_item_by_variant")(time.Now())

	var one int
	err := tx.QueryRow(ctx, `
		SELECT 1 FROM variants WHERE id = $1 AND is_deleted = false`,
		pgUUID(variantID),
	).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, domain.ErrVariantNotFound
		}
		return uuid.Nil, err
	}

	var itemID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM inventory_items WHERE variant_id = $1`,
		pgUUID(variantID),
	).Scan(&itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, domain.ErrInventoryItemNotFound
		}
		return uuid.Nil, err
	}

	return uuid.UUID(itemID.Bytes), nil
}

// LocationExists reports whether a location with the given id exists.
func (r *inventoryRepo) LocationExists(ctx context.Context, tx pgx.Tx, locationID uuid.UUID) (bool, error) {
	defer metrics.ObserveDB("inventory", "location_exists")(time.Now())

	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1)`,
		pgUUID(locationID),
	).Scan(&exists)
	return exists, err
}

// LockInventoryLevel locks the inventory level for an item/location pair with
// SELECT ... FOR UPDATE (spec Section 3.3) and returns it. Returns
// ErrInventoryLevelNotFound when no level row exists. The lock is held until
// the surrounding transaction commits or rolls back.
func (r *inventoryRepo) LockInventoryLevel(ctx context.Context, tx pgx.Tx, inventoryItemID, locationID uuid.UUID) (domain.InventoryLevel, error) {
	defer metrics.ObserveDB("inventory", "lock_inventory_level")(time.Now())

	rows, err := tx.Query(ctx, `
		SELECT
			id,
			inventory_item_id,
			location_id,
			COALESCE(available_qty, 0) AS available_qty,
			COALESCE(reserved_qty, 0) AS reserved_qty,
			COALESCE(updated_at, now()) AS updated_at
		FROM inventory_levels
		WHERE inventory_item_id = $1 AND location_id = $2
		FOR UPDATE`,
		pgUUID(inventoryItemID),
		pgUUID(locationID),
	)
	if err != nil {
		return domain.InventoryLevel{}, err
	}
	defer rows.Close()

	level, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.InventoryLevel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryLevel{}, domain.ErrInventoryLevelNotFound
		}
		return domain.InventoryLevel{}, err
	}
	return level.ToDomain(), nil
}

// LockInventoryLevelByItem locks and returns the single inventory level to
// reserve from for an item when no location was supplied by the caller (spec:
// remove-location-from-reservation.md). The SELECT both picks the best location
// and locks it in one statement, so the choice and the lock are atomic.
// Selection order: default location with enough stock, else highest
// available_qty, tie-broken by location_id ASC. Returns
// ErrInventoryLevelNotFound when the item has no inventory_levels rows at all.
func (r *inventoryRepo) LockInventoryLevelByItem(ctx context.Context, tx pgx.Tx, inventoryItemID uuid.UUID, requiredQty domain.Quantity) (domain.InventoryLevel, error) {
	defer metrics.ObserveDB("inventory", "lock_inventory_level_by_item")(time.Now())

	rows, err := tx.Query(ctx, `
		SELECT
			il.id,
			il.inventory_item_id,
			il.location_id,
			COALESCE(il.available_qty, 0) AS available_qty,
			COALESCE(il.reserved_qty, 0) AS reserved_qty,
			COALESCE(il.updated_at, now()) AS updated_at
		FROM inventory_levels il
		JOIN locations l ON l.id = il.location_id
		WHERE il.inventory_item_id = $1
		ORDER BY
			(l.is_default AND il.available_qty >= $2) DESC,
			il.available_qty DESC,
			il.location_id ASC
		LIMIT 1
		FOR UPDATE OF il`,
		pgUUID(inventoryItemID),
		requiredQty.Int(),
	)
	if err != nil {
		return domain.InventoryLevel{}, err
	}
	defer rows.Close()

	level, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.InventoryLevel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryLevel{}, domain.ErrInventoryLevelNotFound
		}
		return domain.InventoryLevel{}, err
	}
	return level.ToDomain(), nil
}

// UpdateInventoryLevel persists a modified inventory level. It must only be
// called on a level obtained from LockInventoryLevel within the same
// transaction.
func (r *inventoryRepo) UpdateInventoryLevel(ctx context.Context, tx pgx.Tx, level domain.InventoryLevel) error {
	defer metrics.ObserveDB("inventory", "update_inventory_level")(time.Now())

	_, err := tx.Exec(ctx, `
		UPDATE inventory_levels
		SET available_qty = $2, reserved_qty = $3, updated_at = now()
		WHERE id = $1`,
		pgUUID(level.ID),
		level.AvailableQty.Int(),
		level.ReservedQty.Int(),
	)
	return err
}

// InsertStockMove records a stock move.
func (r *inventoryRepo) InsertStockMove(ctx context.Context, tx pgx.Tx, move domain.StockMove) error {
	defer metrics.ObserveDB("inventory", "insert_stock_move")(time.Now())

	_, err := tx.Exec(ctx, `
		INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity, created_by, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		pgUUID(move.ID),
		pgUUID(move.InventoryItemID),
		pgUUIDPtr(move.FromLocationID),
		pgUUIDPtr(move.ToLocationID),
		string(move.MoveType),
		move.Quantity.Int(),
		pgUUIDPtr(move.CreatedBy),
		move.Reason,
	)
	return err
}

// InsertReservation records a reservation row.
func (r *inventoryRepo) InsertReservation(ctx context.Context, tx pgx.Tx, res domain.Reservation) error {
	defer metrics.ObserveDB("inventory", "insert_reservation")(time.Now())

	_, err := tx.Exec(ctx, `
		INSERT INTO reservations (id, inventory_item_id, location_id, order_id, quantity, reserved_at, expires_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		pgUUID(res.ID),
		pgUUID(res.InventoryItemID),
		pgUUID(res.LocationID),
		pgUUIDPtr(res.OrderID),
		res.Quantity.Int(),
		res.ReservedAt,
		res.ExpiresAt,
		string(res.Status),
	)
	return err
}

// TryAcquireIdempotencyKey atomically claims the reservation idempotency key
// for an order (migrations/0003 idempotency_keys). Returns true when this
// transaction inserted the key (it owns the reservation batch) and false when
// a prior request already claimed it. Because the insert happens inside the
// caller's transaction, a failed reservation rolls the claim back too.
func (r *inventoryRepo) TryAcquireIdempotencyKey(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (bool, error) {
	defer metrics.ObserveDB("inventory", "try_acquire_idempotency_key")(time.Now())

	tag, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (order_id)
		VALUES ($1)
		ON CONFLICT (order_id) DO NOTHING`,
		pgUUID(orderID),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// FindActiveReservationsByOrderID returns the ACTIVE reservations for an order,
// locking them FOR UPDATE in deterministic (inventory_item_id, location_id)
// order. Locking serializes concurrent complete/cancel/expire calls so a
// reservation is never processed twice.
func (r *inventoryRepo) FindActiveReservationsByOrderID(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Reservation, error) {
	defer metrics.ObserveDB("inventory", "find_active_reservations_by_order")(time.Now())

	return r.findReservations(ctx, tx, reservationSelect+`
		WHERE r.order_id = $1 AND r.status = 'ACTIVE'
		ORDER BY r.inventory_item_id, r.location_id
		FOR UPDATE OF r`, orderID)
}

// FindReservationsByOrderID returns all reservations for an order (any status).
// Used for idempotent complete/cancel checks (spec Sections 12-16).
func (r *inventoryRepo) FindReservationsByOrderID(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Reservation, error) {
	defer metrics.ObserveDB("inventory", "find_reservations_by_order")(time.Now())

	return r.findReservations(ctx, tx, reservationSelect+`
		WHERE r.order_id = $1
		ORDER BY r.inventory_item_id, r.location_id`, orderID)
}

// UpdateReservationStatus transitions a reservation to the given status,
// stamping released_at when releasedAt is non-nil.
func (r *inventoryRepo) UpdateReservationStatus(ctx context.Context, tx pgx.Tx, reservationID uuid.UUID, status domain.ReservationStatus, releasedAt *time.Time) error {
	defer metrics.ObserveDB("inventory", "update_reservation_status")(time.Now())

	_, err := tx.Exec(ctx, `
		UPDATE reservations
		SET status = $2, released_at = $3
		WHERE id = $1`,
		pgUUID(reservationID),
		string(status),
		releasedAt,
	)
	return err
}

// FindDueActiveReservations returns ACTIVE reservations whose expires_at is
// before now, locking them FOR UPDATE SKIP LOCKED so concurrent expiry workers
// never double-process a row (spec Section 18).
func (r *inventoryRepo) FindDueActiveReservations(ctx context.Context, tx pgx.Tx, now time.Time) ([]domain.Reservation, error) {
	defer metrics.ObserveDB("inventory", "find_due_active_reservations")(time.Now())

	return r.findReservations(ctx, tx, reservationSelect+`
		WHERE r.status = 'ACTIVE' AND r.expires_at IS NOT NULL AND r.expires_at < $1
		ORDER BY r.inventory_item_id, r.location_id
		FOR UPDATE OF r SKIP LOCKED`, now)
}

// findReservations runs a reservation SELECT on the given transaction and maps
// the result rows to domain reservations.
func (r *inventoryRepo) findReservations(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]domain.Reservation, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[model.Reservation])
	if err != nil {
		return nil, err
	}

	reservations := make([]domain.Reservation, 0, len(models))
	for _, m := range models {
		reservations = append(reservations, m.ToDomain())
	}
	return reservations, nil
}
