package repository

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sort"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/metrics"
	"gin-product-service/internal/infrastructure/repository/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// inventoryRepo implements domain.ReservationRepository over PostgreSQL using
// pgx/v5. PostgreSQL is the durable correctness authority; the repository
// performs the idempotency claim, deterministic level locking, quantity
// checks, and every write inside one transaction.
type inventoryRepo struct {
	db database.Database
}

// NewInventoryRepo constructs the reservation repository.
func NewInventoryRepo(db database.Database) domain.ReservationRepository {
	return &inventoryRepo{db: db}
}

var _ domain.ReservationRepository = (*inventoryRepo)(nil)

// resolvedItem couples a resolved inventory item with the caller's public
// variant ID and the generated reservation/move identifiers.
type resolvedItem struct {
	VariantID       uuid.UUID
	InventoryItemID uuid.UUID
	Quantity        domain.Quantity
	ReservationID   uuid.UUID
	StockMoveID     uuid.UUID
}

func (r *inventoryRepo) CreateReservation(ctx context.Context, input domain.CreateReservationInput) (domain.ReservationResult, error) {
	defer metrics.ObserveDB("inventory", "create_reservation")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return domain.ReservationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Serialize every request for one order with a deterministic
	//    transaction-scoped advisory lock. This closes the zero-row race that
	//    FOR UPDATE and the unique (order_id, inventory_item_id) constraint
	//    cannot cover: two simultaneous first requests with different item sets
	//    would otherwise both see no rows and both reserve stock.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, orderAdvisoryLockKey(input.OrderID)); err != nil {
		return domain.ReservationResult{}, err
	}

	// 2. Load the order's persisted reservation set (with public variant IDs)
	//    while holding row locks. Existing rows are the sole durable idempotency
	//    record: a retry returns them, any difference is a conflict.
	persisted, err := r.loadOrderReservations(ctx, tx, input.OrderID)
	if err != nil {
		return domain.ReservationResult{}, err
	}
	if len(persisted.Items) > 0 {
		if orderMatchesRequest(persisted.Items, input) {
			persisted.Retried = true
			return persisted, nil
		}
		return domain.ReservationResult{}, domain.ErrReservationConflict
	}

	// 3. Capture one authoritative creation instant after order coordination.
	var reservedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&reservedAt); err != nil {
		return domain.ReservationResult{}, err
	}

	// 4. Reject an expiry that is not strictly future at the authoritative
	//    creation point, before any quantity transfer.
	if err := domain.ValidateExpiry(input.ExpiresAt, reservedAt); err != nil {
		return domain.ReservationResult{}, err
	}

	// 5. Resolve the single default fulfillment location.
	var locationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM locations WHERE is_default = true LIMIT 1`).Scan(&locationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ReservationResult{}, domain.ErrReservationInventoryNotFound
		}
		return domain.ReservationResult{}, err
	}

	// 6. Resolve each public variant to its tracked, non-deleted inventory item.
	resolved, err := r.resolveItems(ctx, tx, input.Items)
	if err != nil {
		return domain.ReservationResult{}, err
	}

	// 7. Lock each level in deterministic inventory-item order, then check and
	//    transfer quantities while those rows remain locked.
	for _, item := range resolved {
		var available, reserved int
		err = tx.QueryRow(ctx, `
			SELECT available_qty, reserved_qty
			FROM inventory_levels
			WHERE inventory_item_id = $1 AND location_id = $2
			FOR UPDATE`,
			pgUUID(item.InventoryItemID), pgUUID(locationID),
		).Scan(&available, &reserved)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ReservationResult{}, &domain.VariantError{
					VariantID: item.VariantID, Err: domain.ErrReservationInventoryNotFound,
				}
			}
			return domain.ReservationResult{}, err
		}
		if available < item.Quantity.Int() {
			return domain.ReservationResult{}, &domain.VariantError{
				VariantID: item.VariantID, Err: domain.ErrInsufficientStock,
			}
		}

		_, err = tx.Exec(ctx, `
			UPDATE inventory_levels
			SET available_qty = available_qty - $1,
			    reserved_qty  = reserved_qty  + $1,
			    updated_at    = now()
			WHERE inventory_item_id = $2 AND location_id = $3`,
			item.Quantity.Int(), pgUUID(item.InventoryItemID), pgUUID(locationID),
		)
		if err != nil {
			return domain.ReservationResult{}, err
		}
	}

	// 8. Insert one ACTIVE reservation and one RESERVE stock movement
	//    per item, persisting the caller-owned expiry and the shared reserved
	//    instant unchanged.
	result := domain.ReservationResult{
		OrderID: input.OrderID,
		Items:   make([]domain.ReservationItemResult, 0, len(resolved)),
	}
	for _, item := range resolved {
		var dbReservedAt, expiresAt time.Time
		err = tx.QueryRow(ctx, `
			INSERT INTO reservations (id, inventory_item_id, location_id, order_id, quantity, reserved_at, expires_at, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'ACTIVE')
			RETURNING reserved_at, expires_at`,
			pgUUID(item.ReservationID),
			pgUUID(item.InventoryItemID),
			pgUUID(locationID),
			pgUUID(input.OrderID),
			item.Quantity.Int(),
			reservedAt,
			input.ExpiresAt,
		).Scan(&dbReservedAt, &expiresAt)
		if err != nil {
			return domain.ReservationResult{}, err
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
			VALUES ($1, $2, $3, NULL, 'RESERVE', $4)`,
			pgUUID(item.StockMoveID),
			pgUUID(item.InventoryItemID),
			pgUUID(locationID),
			item.Quantity.Int(),
		)
		if err != nil {
			return domain.ReservationResult{}, err
		}

		result.Items = append(result.Items, domain.ReservationItemResult{
			ReservationID: item.ReservationID,
			VariantID:     item.VariantID,
			Quantity:      item.Quantity,
			Status:        domain.ReservationActive,
			ExpiresAt:     expiresAt,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.ReservationResult{}, err
	}

	return result, nil
}

// resolveItems resolves every requested variant to a non-deleted, tracked
// inventory item and returns them sorted by inventory item ID for deterministic
// locking.
func (r *inventoryRepo) resolveItems(ctx context.Context, tx pgx.Tx, items []domain.CreateReservationItem) ([]resolvedItem, error) {
	resolved := make([]resolvedItem, 0, len(items))
	for _, it := range items {
		var itemID uuid.UUID
		var track, isDeleted bool
		err := tx.QueryRow(ctx, `
			SELECT ii.id, ii.track_inventory, v.is_deleted
			FROM inventory_items ii
			JOIN variants v ON v.id = ii.variant_id
			WHERE ii.variant_id = $1`,
			pgUUID(it.VariantID),
		).Scan(&itemID, &track, &isDeleted)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, &domain.VariantError{VariantID: it.VariantID, Err: domain.ErrReservationVariantNotFound}
			}
			return nil, err
		}
		if isDeleted || !track {
			return nil, &domain.VariantError{VariantID: it.VariantID, Err: domain.ErrVariantNotReservable}
		}
		resolved = append(resolved, resolvedItem{
			VariantID:       it.VariantID,
			InventoryItemID: itemID,
			Quantity:        it.Quantity,
			ReservationID:   it.ReservationID,
			StockMoveID:     it.StockMoveID,
		})
	}

	sort.Slice(resolved, func(i, j int) bool {
		return bytes.Compare(resolved[i].InventoryItemID[:], resolved[j].InventoryItemID[:]) < 0
	})
	return resolved, nil
}

// loadOrderReservations returns the persisted reservation set for one order,
// joined to public variant IDs and locked FOR UPDATE, without writing anything.
func (r *inventoryRepo) loadOrderReservations(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (domain.ReservationResult, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.id, r.inventory_item_id, r.location_id, r.order_id, r.quantity,
		       r.reserved_at, r.expires_at, r.status, r.released_at, ii.variant_id
		FROM reservations r
		JOIN inventory_items ii ON ii.id = r.inventory_item_id
		WHERE r.order_id = $1
		ORDER BY r.inventory_item_id
		FOR UPDATE OF r`,
		pgUUID(orderID),
	)
	if err != nil {
		return domain.ReservationResult{}, err
	}
	defer rows.Close()

	reservationRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[model.Reservation])
	if err != nil {
		return domain.ReservationResult{}, err
	}

	result := domain.ReservationResult{
		OrderID: orderID,
		Items:   make([]domain.ReservationItemResult, 0, len(reservationRows)),
	}
	for _, row := range reservationRows {
		item, ok := row.ToResultItem()
		if !ok {
			return domain.ReservationResult{}, domain.ErrReservationInventoryNotFound
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// orderMatchesRequest reports whether the persisted reservation set has the
// identical canonical item set, quantities, and represented expiry instant as
// the request. Caller item order and equivalent offset spellings are
// irrelevant: both sides are sorted by public variant ID and compared by
// instant equality.
func orderMatchesRequest(persisted []domain.ReservationItemResult, input domain.CreateReservationInput) bool {
	if len(persisted) != len(input.Items) {
		return false
	}

	req := append([]domain.CreateReservationItem(nil), input.Items...)
	sort.Slice(req, func(i, j int) bool { return bytes.Compare(req[i].VariantID[:], req[j].VariantID[:]) < 0 })

	pers := append([]domain.ReservationItemResult(nil), persisted...)
	sort.Slice(pers, func(i, j int) bool { return bytes.Compare(pers[i].VariantID[:], pers[j].VariantID[:]) < 0 })

	for i := range req {
		if req[i].VariantID != pers[i].VariantID || req[i].Quantity != pers[i].Quantity {
			return false
		}
		if !pers[i].ExpiresAt.Equal(input.ExpiresAt) {
			return false
		}
	}
	return true
}

// orderAdvisoryLockKey folds the complete 16-byte order ID into a single int64
// advisory-lock key. A fold can only serialize unrelated orders; it can never
// return a wrong result because the reservation rows remain the durable
// correctness record.
func orderAdvisoryLockKey(orderID uuid.UUID) int64 {
	hi := binary.BigEndian.Uint64(orderID[0:8])
	lo := binary.BigEndian.Uint64(orderID[8:16])
	return int64(hi ^ lo)
}
