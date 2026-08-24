package inventory

import (
	"context"
	"sort"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type reservationUc struct {
	repo domain.InventoryRepository
}

// NewReservationUseCase constructs the reservation use case
// (spec Sections 7-17).
func NewReservationUseCase(repo domain.InventoryRepository) domain.ReservationUseCase {
	return &reservationUc{repo: repo}
}

// Create reserves one or more variants for an order in a single transaction
// (spec Sections 7-11). If any item cannot be reserved, the whole transaction
// rolls back — no partial reservations. Creating again for an order that
// already has an ACTIVE batch returns the existing reservations (idempotent,
// spec Sections 9 and 26).
func (uc *reservationUc) Create(ctx context.Context, input domain.CreateReservationInput) (domain.ReservationResult, error) {
	if err := validateReservationInput(input); err != nil {
		return domain.ReservationResult{}, err
	}

	var result domain.ReservationResult
	err := uc.repo.WithTx(ctx, func(ctx context.Context, tx domain.Tx) error {
		// Idempotency (spec Sections 9, 26): atomically claim the order's
		// idempotency key. Only the first request proceeds; retries (including
		// concurrent races, which serialize on the key's primary key) return
		// the existing reservations instead of double-reserving.
		acquired, err := uc.repo.TryAcquireIdempotencyKey(ctx, tx, input.OrderID)
		if err != nil {
			return err
		}
		if !acquired {
			existing, err := uc.repo.FindReservationsByOrderID(ctx, tx, input.OrderID)
			if err != nil {
				return err
			}
			if len(existing) == 0 {
				return domain.ErrDuplicateActiveReservation
			}
			result = reservationResultFrom(input.OrderID, existing)
			return nil
		}

		now := time.Now().UTC()

		// Pass 1a: resolve every item and verify every location before taking
		// any lock, so a bad variant/location fails without touching state.
		type resolvedItem struct {
			variantID  uuid.UUID
			itemID     uuid.UUID
			locationID uuid.UUID
			quantity   int
		}
		resolved := make([]resolvedItem, 0, len(input.Items))
		for _, item := range input.Items {
			itemID, err := uc.repo.ResolveInventoryItemIDByVariant(ctx, tx, item.VariantID)
			if err != nil {
				return err
			}
			exists, err := uc.repo.LocationExists(ctx, tx, item.LocationID)
			if err != nil {
				return err
			}
			if !exists {
				return domain.ErrLocationNotFound
			}
			resolved = append(resolved, resolvedItem{
				variantID:  item.VariantID,
				itemID:     itemID,
				locationID: item.LocationID,
				quantity:   item.Quantity,
			})
		}

		// Pass 1b: lock all inventory levels in deterministic
		// (inventory_item_id, location_id) order (spec Section 22) to avoid
		// deadlocks between concurrent reservations.
		sort.Slice(resolved, func(i, j int) bool {
			if resolved[i].itemID != resolved[j].itemID {
				return resolved[i].itemID.String() < resolved[j].itemID.String()
			}
			return resolved[i].locationID.String() < resolved[j].locationID.String()
		})

		type lockedItem struct {
			reservation domain.Reservation
			level       domain.InventoryLevel
		}
		locked := make([]lockedItem, 0, len(resolved))
		for _, r := range resolved {
			level, err := uc.repo.LockInventoryLevel(ctx, tx, r.itemID, r.locationID)
			if err != nil {
				return err
			}
			locked = append(locked, lockedItem{
				reservation: domain.Reservation{
					ID:              uuid.Must(uuid.NewV7()),
					InventoryItemID: r.itemID,
					VariantID:       r.variantID,
					LocationID:      r.locationID,
					OrderID:         &input.OrderID,
					Quantity:        domain.Quantity(r.quantity),
					ReservedAt:      now,
					ExpiresAt:       input.ExpiresAt,
					Status:          domain.ReservationActive,
				},
				level: level,
			})
		}

		// Pass 2: check availability for every item before mutating anything.
		for _, l := range locked {
			if l.level.AvailableQty < l.reservation.Quantity {
				return domain.ErrInsufficientStock
			}
		}

		// Pass 3: apply all level changes and insert all reservation rows.
		for i := range locked {
			locked[i].level.AvailableQty -= locked[i].reservation.Quantity
			locked[i].level.ReservedQty += locked[i].reservation.Quantity
			if err := uc.repo.UpdateInventoryLevel(ctx, tx, locked[i].level); err != nil {
				return err
			}
			if err := uc.repo.InsertReservation(ctx, tx, locked[i].reservation); err != nil {
				return err
			}
		}

		reservations := make([]domain.Reservation, 0, len(locked))
		for _, l := range locked {
			reservations = append(reservations, l.reservation)
		}
		result = domain.ReservationResult{
			OrderID:      input.OrderID,
			Status:       domain.ReservationActive,
			Reservations: reservations,
		}
		return nil
	})
	if err != nil {
		logger.L(ctx).Error("create reservation failed", zap.Error(err), zap.String("order_id", input.OrderID.String()))
		return domain.ReservationResult{}, err
	}

	return result, nil
}

// Complete transitions all ACTIVE reservations of an order to COMPLETED,
// consuming the reserved stock (spec Sections 12-14). It is idempotent: a
// second call for an already-completed order is a no-op returning its current
// state; an order that never had a reservation returns ErrReservationNotFound.
func (uc *reservationUc) Complete(ctx context.Context, orderID uuid.UUID) (domain.ReservationResult, error) {
	var result domain.ReservationResult
	err := uc.repo.WithTx(ctx, func(ctx context.Context, tx domain.Tx) error {
		active, err := uc.repo.FindActiveReservationsByOrderID(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			// Nothing ACTIVE: idempotent no-op if the order ever had a
			// reservation, otherwise not found (spec Section 14).
			all, err := uc.repo.FindReservationsByOrderID(ctx, tx, orderID)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				return domain.ErrReservationNotFound
			}
			result = reservationResultFrom(orderID, all)
			return nil
		}

		now := time.Now().UTC()
		for i := range active {
			// Reservation rows are already locked by the find query; lock the
			// level rows (same deterministic order) before mutating.
			level, err := uc.repo.LockInventoryLevel(ctx, tx, active[i].InventoryItemID, active[i].LocationID)
			if err != nil {
				return err
			}
			if level.ReservedQty < active[i].Quantity {
				// Defensive: reserved_qty must never go negative (spec 19).
				return domain.ErrInsufficientStock
			}
			// Completion consumes reserved stock; available_qty is untouched
			// (spec Section 13).
			level.ReservedQty -= active[i].Quantity
			if err := uc.repo.UpdateInventoryLevel(ctx, tx, level); err != nil {
				return err
			}
			if err := uc.repo.UpdateReservationStatus(ctx, tx, active[i].ID, domain.ReservationCompleted, &now); err != nil {
				return err
			}
			active[i].Status = domain.ReservationCompleted
			active[i].ReleasedAt = &now
		}
		result = domain.ReservationResult{
			OrderID:      orderID,
			Status:       domain.ReservationCompleted,
			Reservations: active,
		}
		return nil
	})
	if err != nil {
		logger.L(ctx).Error("complete reservation failed", zap.Error(err), zap.String("order_id", orderID.String()))
		return domain.ReservationResult{}, err
	}
	return result, nil
}

// Cancel transitions all ACTIVE reservations of an order to CANCELLED,
// returning the reserved stock to available (spec Sections 15-17). It is
// idempotent: a second call for an already-cancelled order is a no-op.
func (uc *reservationUc) Cancel(ctx context.Context, orderID uuid.UUID) (domain.ReservationResult, error) {
	var result domain.ReservationResult
	err := uc.repo.WithTx(ctx, func(ctx context.Context, tx domain.Tx) error {
		active, err := uc.repo.FindActiveReservationsByOrderID(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			all, err := uc.repo.FindReservationsByOrderID(ctx, tx, orderID)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				return domain.ErrReservationNotFound
			}
			result = reservationResultFrom(orderID, all)
			return nil
		}

		now := time.Now().UTC()
		for i := range active {
			level, err := uc.repo.LockInventoryLevel(ctx, tx, active[i].InventoryItemID, active[i].LocationID)
			if err != nil {
				return err
			}
			if level.ReservedQty < active[i].Quantity {
				return domain.ErrInsufficientStock
			}
			// Cancellation returns reserved stock to available (spec 16).
			level.ReservedQty -= active[i].Quantity
			level.AvailableQty += active[i].Quantity
			if err := uc.repo.UpdateInventoryLevel(ctx, tx, level); err != nil {
				return err
			}
			if err := uc.repo.UpdateReservationStatus(ctx, tx, active[i].ID, domain.ReservationCancelled, &now); err != nil {
				return err
			}
			active[i].Status = domain.ReservationCancelled
			active[i].ReleasedAt = &now
		}
		result = domain.ReservationResult{
			OrderID:      orderID,
			Status:       domain.ReservationCancelled,
			Reservations: active,
		}
		return nil
	})
	if err != nil {
		logger.L(ctx).Error("cancel reservation failed", zap.Error(err), zap.String("order_id", orderID.String()))
		return domain.ReservationResult{}, err
	}
	return result, nil
}

// reservationResultFrom builds an idempotent no-op result from an order's
// existing (non-active) reservations, deriving the batch status from them.
func reservationResultFrom(orderID uuid.UUID, reservations []domain.Reservation) domain.ReservationResult {
	status := domain.ReservationActive
	if len(reservations) > 0 {
		status = reservations[0].Status
	}
	return domain.ReservationResult{
		OrderID:      orderID,
		Status:       status,
		Reservations: reservations,
	}
}

// validateReservationInput applies spec Section 9 validation.
func validateReservationInput(input domain.CreateReservationInput) error {
	if input.OrderID == uuid.Nil {
		return domain.ErrInvalidReservationInput
	}
	if len(input.Items) == 0 {
		return domain.ErrInvalidReservationInput
	}
	for _, item := range input.Items {
		if item.VariantID == uuid.Nil || item.LocationID == uuid.Nil || item.Quantity <= 0 {
			return domain.ErrInvalidReservationInput
		}
	}
	return nil
}
