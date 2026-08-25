package inventory

import (
	"context"
	"fmt"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type stockMoveUc struct {
	repo domain.InventoryRepository
}

// NewStockMoveUseCase constructs the stock-move use case (spec Sections 4-6).
func NewStockMoveUseCase(repo domain.InventoryRepository) domain.StockMoveUseCase {
	return &stockMoveUc{repo: repo}
}

// Create records a stock move and applies the corresponding inventory level
// change atomically (spec Section 3.2). The whole operation runs in one
// transaction; any failure rolls back every statement.
//
// track_inventory policy: inventory quantities are always updated. This is
// consistent with the product module, which always seeds inventory_levels rows
// regardless of track_inventory.
func (uc *stockMoveUc) Create(ctx context.Context, input domain.CreateStockMoveInput) (domain.StockMove, error) {
	if err := validateStockMoveInput(input); err != nil {
		return domain.StockMove{}, err
	}

	var move domain.StockMove
	err := uc.repo.WithTx(ctx, func(ctx context.Context, tx domain.Tx) error {
		itemID, err := uc.repo.ResolveInventoryItemIDByVariant(ctx, tx, input.VariantID)
		if err != nil {
			return err
		}

		switch input.MoveType {
		case domain.StockMoveIn:
			toLoc := *input.ToLocationID
			if err := uc.requireLocation(ctx, tx, toLoc); err != nil {
				fmt.Println("err validation 1")
				return err
			}
			fmt.Println("itemID >>>> ", itemID)
			level, err := uc.repo.LockInventoryLevel(ctx, tx, itemID, toLoc)
			if err != nil {
				fmt.Println("err validation 2")
				return err
			}
			level.AvailableQty += domain.Quantity(input.Quantity)
			if err := uc.repo.UpdateInventoryLevel(ctx, tx, level); err != nil {
				fmt.Println("err validation 3")
				return err
			}

		case domain.StockMoveOut:
			fromLoc := *input.FromLocationID
			if err := uc.requireLocation(ctx, tx, fromLoc); err != nil {
				return err
			}
			level, err := uc.repo.LockInventoryLevel(ctx, tx, itemID, fromLoc)
			if err != nil {
				return err
			}
			if level.AvailableQty < domain.Quantity(input.Quantity) {
				return domain.ErrInsufficientStock
			}
			level.AvailableQty -= domain.Quantity(input.Quantity)
			if err := uc.repo.UpdateInventoryLevel(ctx, tx, level); err != nil {
				return err
			}

		case domain.StockMoveTransfer:
			fromLoc, toLoc := *input.FromLocationID, *input.ToLocationID
			if err := uc.requireLocation(ctx, tx, fromLoc); err != nil {
				return err
			}
			if err := uc.requireLocation(ctx, tx, toLoc); err != nil {
				return err
			}
			// Lock both level rows in deterministic location order (spec
			// Section 22) to avoid deadlocks when transfers race.
			// first, second := fromLoc, toLoc
			// if second.String() < first.String() {
			// 	first, second = second, first
			// }
			// levelA, err := uc.repo.LockInventoryLevel(ctx, tx, itemID, first)
			// if err != nil {
			// 	return err
			// }
			// levelB, err := uc.repo.LockInventoryLevel(ctx, tx, itemID, second)
			// if err != nil {
			// 	return err
			// }
			// // Identify source (from) and destination (to) regardless of lock order.
			// var src, dst *domain.InventoryLevel
			// if levelA.LocationID == fromLoc {
			// 	src, dst = &levelA, &levelB
			// } else {
			// 	src, dst = &levelB, &levelA
			// }
			// if src.AvailableQty < domain.Quantity(input.Quantity) {
			// 	return domain.ErrInsufficientStock
			// }
			// src.AvailableQty -= domain.Quantity(input.Quantity)
			// dst.AvailableQty += domain.Quantity(input.Quantity)
			// if err := uc.repo.UpdateInventoryLevel(ctx, tx, levelA); err != nil {
			// 	return err
			// }
			// if err := uc.repo.UpdateInventoryLevel(ctx, tx, levelB); err != nil {
			// 	return err
			// }

		case domain.StockMoveAdjust:
			toLoc := *input.ToLocationID
			if err := uc.requireLocation(ctx, tx, toLoc); err != nil {
				return err
			}
			level, err := uc.repo.LockInventoryLevel(ctx, tx, itemID, toLoc)
			if err != nil {
				return err
			}
			// ADJUST treats quantity as the desired absolute available quantity
			// (spec Sections 4.2, 5.4); the delta is implicit.
			level.AvailableQty = domain.Quantity(input.Quantity)
			if err := uc.repo.UpdateInventoryLevel(ctx, tx, level); err != nil {
				return err
			}
		}

		now := time.Now().UTC()
		move = domain.StockMove{
			ID:              uuid.Must(uuid.NewV7()),
			InventoryItemID: itemID,
			FromLocationID:  input.FromLocationID,
			ToLocationID:    input.ToLocationID,
			MoveType:        input.MoveType,
			Quantity:        domain.Quantity(input.Quantity),
			CreatedBy:       input.CreatedBy,
			Reason:          input.Reason,
			CreatedAt:       now,
		}
		return uc.repo.InsertStockMove(ctx, tx, move)
	})
	if err != nil {
		logger.L(ctx).Error("create stock move failed", zap.Error(err))
		return domain.StockMove{}, err
	}

	return move, nil
}

// requireLocation verifies a location exists, returning ErrLocationNotFound
// otherwise (spec Section 21).
func (uc *stockMoveUc) requireLocation(ctx context.Context, tx domain.Tx, locationID uuid.UUID) error {
	exists, err := uc.repo.LocationExists(ctx, tx, locationID)
	if err != nil {
		return err
	}
	if !exists {
		return domain.ErrLocationNotFound
	}
	return nil
}

// validateStockMoveInput applies the common and move-type-specific validation
// from spec Section 4.2.
func validateStockMoveInput(input domain.CreateStockMoveInput) error {
	if input.VariantID == uuid.Nil {
		return domain.ErrVariantIDRequired
	}
	if input.Quantity <= 0 {
		return domain.ErrInvalidQuantity
	}

	switch input.MoveType {
	case domain.StockMoveIn:
		if input.ToLocationID == nil {
			return domain.ErrToLocationRequired
		}
	case domain.StockMoveOut:
		if input.FromLocationID == nil {
			return domain.ErrFromLocationRequired
		}
	case domain.StockMoveTransfer:
		if input.FromLocationID == nil {
			return domain.ErrFromLocationRequired
		}
		if input.ToLocationID == nil {
			return domain.ErrToLocationRequired
		}
		if *input.FromLocationID == *input.ToLocationID {
			return domain.ErrFromToLocationSame
		}
	case domain.StockMoveAdjust:
		if input.ToLocationID == nil {
			return domain.ErrToLocationRequired
		}
	default:
		// Unsupported move type (RESERVE/UNRESERVE are not API-exposed,
		// spec Section 2.4).
		return domain.ErrInvalidStockMoveType
	}
	return nil
}
