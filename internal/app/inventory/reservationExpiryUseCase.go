package inventory

import (
	"context"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"go.uber.org/zap"
)

type reservationExpiryUc struct {
	repo domain.InventoryRepository
}

// NewReservationExpiryUseCase constructs the expiry use case
// (spec Section 18). It is intended for a background worker, not an HTTP
// handler.
func NewReservationExpiryUseCase(repo domain.InventoryRepository) domain.ReservationExpiryUseCase {
	return &reservationExpiryUc{repo: repo}
}

// ExpireDue expires every ACTIVE reservation whose expires_at has passed,
// applying the same state transition as cancellation (reserved back to
// available, status = EXPIRED). Returns the number of reservations expired.
// It runs in one transaction and is idempotent: rows are locked with
// FOR UPDATE SKIP LOCKED so concurrent workers never double-expire.
func (uc *reservationExpiryUc) ExpireDue(ctx context.Context) (int, error) {
	var expiredCount int
	err := uc.repo.WithTx(ctx, func(ctx context.Context, tx domain.Tx) error {
		due, err := uc.repo.FindDueActiveReservations(ctx, tx, time.Now().UTC())
		if err != nil {
			return err
		}
		if len(due) == 0 {
			return nil
		}

		now := time.Now().UTC()
		for i := range due {
			level, err := uc.repo.LockInventoryLevel(ctx, tx, due[i].InventoryItemID, due[i].LocationID)
			if err != nil {
				return err
			}
			if level.ReservedQty < due[i].Quantity {
				return domain.ErrInsufficientStock
			}
			level.ReservedQty -= due[i].Quantity
			level.AvailableQty += due[i].Quantity
			if err := uc.repo.UpdateInventoryLevel(ctx, tx, level); err != nil {
				return err
			}
			if err := uc.repo.UpdateReservationStatus(ctx, tx, due[i].ID, domain.ReservationExpired, &now); err != nil {
				return err
			}
			expiredCount++
		}
		return nil
	})
	if err != nil {
		logger.L(ctx).Error("expire reservations failed", zap.Error(err))
		return 0, err
	}
	return expiredCount, nil
}
