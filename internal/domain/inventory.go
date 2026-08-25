package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StockMoveType enumerates the allowed stock movement kinds.
type StockMoveType string

const (
	StockMoveIn        StockMoveType = "IN"
	StockMoveOut       StockMoveType = "OUT"
	StockMoveTransfer  StockMoveType = "TRANSFER"
	StockMoveAdjust    StockMoveType = "ADJUST"
	StockMoveReserve   StockMoveType = "RESERVE"
	StockMoveUnreserve StockMoveType = "UNRESERVE"
)

var (
	// ErrInvalidQuantity is returned when a negative quantity is passed to an
	// inventory-mutating method.
	ErrInvalidQuantity = errors.New("quantity must not be negative")
	// ErrInsufficientStock is returned when a reservation would exceed the
	// currently available quantity.
	ErrInsufficientStock = errors.New("insufficient available stock")
)

// Quantity is a non-negative count of stock units. Constructing one via
// NewQuantity is the compile-time-adjacent guarantee that "stock can't go
// negative" — callers that already validated non-negative input (e.g. rows
// read back from this app's own database) may convert directly via
// Quantity(n) instead of re-validating trusted data.
type Quantity int

// NewQuantity constructs a Quantity, rejecting negative input.
func NewQuantity(n int) (Quantity, error) {
	if n < 0 {
		return 0, ErrInvalidQuantity
	}
	return Quantity(n), nil
}

// Int returns the underlying int, for arithmetic and passing to SQL query args.
func (q Quantity) Int() int {
	return int(q)
}

type InventoryItem struct {
	ID             uuid.UUID  `json:"id"`
	VariantID      *uuid.UUID `json:"variant_id,omitempty"`
	Description    *string    `json:"description,omitempty"`
	TrackInventory bool       `json:"track_inventory"`
	CreatedAt      time.Time  `json:"created_at"`
}

type InventoryLevel struct {
	ID              uuid.UUID `json:"id"`
	InventoryItemID uuid.UUID `json:"inventory_item_id"`
	LocationID      uuid.UUID `json:"location_id"`
	AvailableQty    Quantity  `json:"available_qty"`
	ReservedQty     Quantity  `json:"reserved_qty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Reserve moves qty units from AvailableQty to ReservedQty. It returns
// ErrInvalidQuantity if qty is negative, or ErrInsufficientStock if qty
// exceeds the currently available quantity. On success it mutates l in place.
func (l *InventoryLevel) Reserve(qty Quantity) error {
	if qty < 0 {
		return ErrInvalidQuantity
	}
	if qty > l.AvailableQty {
		return ErrInsufficientStock
	}
	l.AvailableQty -= qty
	l.ReservedQty += qty
	return nil
}

type StockMove struct {
	ID              uuid.UUID     `json:"id"`
	InventoryItemID uuid.UUID     `json:"inventory_item_id"`
	FromLocationID  *uuid.UUID    `json:"from_location_id,omitempty"`
	ToLocationID    *uuid.UUID    `json:"to_location_id,omitempty"`
	MoveType        StockMoveType `json:"move_type"`
	Quantity        Quantity      `json:"quantity"`
	CreatedBy       *uuid.UUID    `json:"created_by,omitempty"`
	Reason          *string       `json:"reason,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
}

// Location represents a warehouse, store, or other physical stocking point
// (spec Section 2.2).
type Location struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	Name      string          `json:"name" db:"name"`
	Type      *string         `json:"type,omitempty" db:"type"`
	Address   json.RawMessage `json:"address,omitempty" db:"address"`
	IsDefault bool            `json:"is_default" db:"is_default"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}

// Errors surfaced by the inventory module. ErrVariantNotFound, ErrInvalidQuantity
// and ErrInsufficientStock are defined elsewhere in this package and reused.
var (
	// ErrInvalidStockMoveInput is returned when a stock move request fails basic
	// validation (spec Section 21: quantity <= 0, invalid move type, missing
	// required location).
	ErrInvalidStockMoveInput = errors.New("invalid stock move input")
	// ErrInventoryItemNotFound is returned when a variant has no inventory item.
	ErrInventoryItemNotFound = errors.New("inventory item not found")
	// ErrLocationNotFound is returned when a referenced location does not exist.
	ErrLocationNotFound = errors.New("location not found")
	// ErrInventoryLevelNotFound is returned when no inventory level exists for
	// an item/location pair.
	ErrInventoryLevelNotFound = errors.New("inventory level not found")
	// ErrFromToLocationSame is returned when a TRANSFER references the same
	// from and to location.
	ErrFromToLocationSame = errors.New("from and to location must be different")

	// Domain Business Validation Errors
	ErrVariantIDRequired = errors.New("variant ID is required")
	// ErrInvalidQuantity       = errors.New("quantity must be greater than zero")
	ErrFromLocationRequired = errors.New("from_location_id is required for OUT and TRANSFER moves")
	ErrToLocationRequired   = errors.New("to_location_id is required for IN, TRANSFER, and ADJUST moves")
	// ErrFromToLocationSame    = errors.New("from and to location must be different")
	ErrInvalidStockMoveType = errors.New("unsupported or invalid stock move type")

	// Domain State Errors
	// ErrInventoryItemNotFound  = errors.New("inventory item not found")
	// ErrLocationNotFound       = errors.New("location not found")
	// ErrInventoryLevelNotFound = errors.New("inventory level not found")
)

// Tx is the transaction handle passed to transaction-scoped repository methods.
// It is an alias of pgx.Tx so use cases can reference it without importing pgx
// directly.
type Tx = pgx.Tx

// CreateStockMoveInput is the request payload for creating a stock move
// (spec Section 4.1). Move-type specific validation is applied by the use case.
type CreateStockMoveInput struct {
	VariantID      uuid.UUID     `json:"variant_id"`
	MoveType       StockMoveType `json:"move_type"`
	Quantity       int           `json:"quantity"`
	FromLocationID *uuid.UUID    `json:"from_location_id"`
	ToLocationID   *uuid.UUID    `json:"to_location_id"`
	Reason         *string       `json:"reason"`
	CreatedBy      *uuid.UUID    `json:"created_by"`
}

// StockMoveUseCase is the application-layer contract for recording a stock move
// (spec Sections 4-6).
type StockMoveUseCase interface {
	Create(ctx context.Context, input CreateStockMoveInput) (StockMove, error)
}

// InventoryRepository is the persistence contract for the inventory module.
//
// Every method that participates in a multi-statement operation takes a
// pgx.Tx obtained from WithTx; callers must never pass a transaction across two
// WithTx invocations.
//
// Note: pgx.Tx appears in this domain interface as a deliberate, pragmatic
// exception to the "no infrastructure types in domain" rule. Inventory
// operations are inherently transactional (spec Section 3.2) and the use cases
// orchestrate several statements inside a single transaction, so the repository
// exposes a caller-supplied transaction handle rather than hiding it behind a
// coarse-grained method.
type InventoryRepository interface {
	// WithTx runs fn inside a single database transaction. It commits when fn
	// returns nil and rolls back otherwise. All other methods that accept a
	// Tx must be invoked from inside fn.
	WithTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error

	// ResolveInventoryItemIDByVariant returns the inventory_item.id for a
	// variant, verifying the variant exists first (spec Section 3.1).
	ResolveInventoryItemIDByVariant(ctx context.Context, tx Tx, variantID uuid.UUID) (uuid.UUID, error)

	// LocationExists reports whether a location with the given id exists.
	LocationExists(ctx context.Context, tx Tx, locationID uuid.UUID) (bool, error)

	// LockInventoryLevel locks and returns the inventory level for an
	// item/location pair (SELECT ... FOR UPDATE, spec Section 3.3). It returns
	// ErrInventoryLevelNotFound when no level row exists.
	LockInventoryLevel(ctx context.Context, tx Tx, inventoryItemID, locationID uuid.UUID) (InventoryLevel, error)

	// LockInventoryLevelByItem locks and returns the single inventory level to
	// reserve from for an item when no location was supplied by the caller
	// (spec: remove-location-from-reservation.md). Selection prefers the
	// is_default location if it has enough stock, otherwise the location with
	// the highest available_qty, tie-broken by location_id ASC. Returns
	// ErrInventoryLevelNotFound when the item has no inventory_levels rows.
	LockInventoryLevelByItem(ctx context.Context, tx Tx, inventoryItemID uuid.UUID, requiredQty Quantity) (InventoryLevel, error)

	// UpdateInventoryLevel persists a modified inventory level.
	UpdateInventoryLevel(ctx context.Context, tx Tx, level InventoryLevel) error

	// InsertStockMove records a stock move.
	InsertStockMove(ctx context.Context, tx Tx, move StockMove) error

	// InsertReservation records a reservation row.
	InsertReservation(ctx context.Context, tx Tx, r Reservation) error

	// TryAcquireIdempotencyKey atomically claims the reservation idempotency key
	// for an order (idempotency_keys table). It returns true for the request
	// that wins the claim and false when a prior request already claimed it;
	// the claim is rolled back if the surrounding transaction fails.
	TryAcquireIdempotencyKey(ctx context.Context, tx Tx, orderID uuid.UUID) (bool, error)

	// FindActiveReservationsByOrderID returns the ACTIVE reservations for an
	// order, locking them FOR UPDATE in deterministic (item, location) order.
	FindActiveReservationsByOrderID(ctx context.Context, tx Tx, orderID uuid.UUID) ([]Reservation, error)

	// FindReservationsByOrderID returns all reservations for an order (any
	// status), used for idempotent complete/cancel checks.
	FindReservationsByOrderID(ctx context.Context, tx Tx, orderID uuid.UUID) ([]Reservation, error)

	// UpdateReservationStatus transitions a reservation to the given status,
	// stamping released_at when releasedAt is non-nil.
	UpdateReservationStatus(ctx context.Context, tx Tx, reservationID uuid.UUID, status ReservationStatus, releasedAt *time.Time) error

	// FindDueActiveReservations returns ACTIVE reservations whose expires_at is
	// before now, locking them FOR UPDATE SKIP LOCKED (for the expiry worker).
	FindDueActiveReservations(ctx context.Context, tx Tx, now time.Time) ([]Reservation, error)
}
