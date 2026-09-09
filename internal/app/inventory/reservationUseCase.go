package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/metrics"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	reservationOrderKeyPrefix   = "reservation:order:"
	reservationVariantKeyPrefix = "reservation:variant:"
)

// createReservationUc orchestrates one checkout reservation: it validates and
// canonicalizes the request, acquires a short-lived cross-instance lease, and
// delegates the durable all-or-nothing work to the repository.
type createReservationUc struct {
	repo   domain.ReservationRepository
	locker domain.ReservationLocker
}

// NewCreateReservationUseCase constructs the reservation use case.
func NewCreateReservationUseCase(repo domain.ReservationRepository, locker domain.ReservationLocker) domain.CreateReservationUseCase {
	return &createReservationUc{repo: repo, locker: locker}
}

var _ domain.CreateReservationUseCase = (*createReservationUc)(nil)

func (uc *createReservationUc) Create(ctx context.Context, orderID uuid.UUID, items []domain.ReservationRequestItem) (domain.ReservationResult, error) {
	start := time.Now()

	if orderID == uuid.Nil {
		return domain.ReservationResult{}, domain.ErrReservationValidation
	}
	if err := domain.ValidateReservationItems(items); err != nil {
		return domain.ReservationResult{}, err
	}

	// Canonicalize: sort by variant UUID so the fingerprint and lease keys are
	// deterministic regardless of caller ordering.
	sorted := append([]domain.ReservationRequestItem(nil), items...)
	sort.Slice(sorted, func(i, j int) bool { return uuidLess(sorted[i].VariantID, sorted[j].VariantID) })

	fingerprint := fingerprintRequest(sorted)

	// Canonical key set: the order key followed by all variant keys in lexical
	// (== UUID byte) order.
	keys := make([]string, 0, len(sorted)+1)
	keys = append(keys, reservationOrderKeyPrefix+orderID.String())
	for _, it := range sorted {
		keys = append(keys, reservationVariantKeyPrefix+it.VariantID.String())
	}

	// Fail-closed coordination: no lease means no inventory transaction.
	owner, err := uc.locker.Acquire(ctx, keys)
	if err != nil {
		metrics.ReservationAttempts.WithLabelValues("coordination_failed").Inc()
		metrics.ReservationDuration.WithLabelValues("coordination_failed").Observe(time.Since(start).Seconds())
		logger.L(ctx).Warn("reservation coordination unavailable", zap.String("order_id", orderID.String()))
		return domain.ReservationResult{}, domain.ErrReservationCoordinationUnavailable
	}

	// Best-effort, token-checked release after every outcome. A failed release
	// after commit is left to TTL expiry; committed data is never undone.
	defer func() {
		if relErr := uc.locker.Release(context.WithoutCancel(ctx), keys, owner); relErr != nil {
			logger.L(ctx).Warn("reservation lease release failed", zap.String("order_id", orderID.String()))
		}
	}()

	input := domain.CreateReservationInput{
		OrderID:     orderID,
		Fingerprint: fingerprint,
		Items:       make([]domain.CreateReservationItem, 0, len(sorted)),
	}
	for _, it := range sorted {
		input.Items = append(input.Items, domain.CreateReservationItem{
			VariantID:     it.VariantID,
			Quantity:      it.Quantity,
			ReservationID: uuid.Must(uuid.NewV7()),
			StockMoveID:   uuid.Must(uuid.NewV7()),
		})
	}

	result, err := uc.repo.CreateReservation(ctx, input)
	if err != nil {
		uc.recordFailure(ctx, orderID, err, start)
		return domain.ReservationResult{}, err
	}

	outcome := "succeeded"
	if result.Retried {
		outcome = "retried"
	}
	metrics.ReservationAttempts.WithLabelValues(outcome).Inc()
	metrics.ReservationDuration.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
	logger.L(ctx).Info("reservation created",
		zap.String("outcome", outcome),
		zap.String("order_id", orderID.String()),
		zap.Int("items", len(result.Items)),
	)
	return result, nil
}

func (uc *createReservationUc) recordFailure(ctx context.Context, orderID uuid.UUID, err error, start time.Time) {
	outcome := "rejected"
	switch {
	case errors.Is(err, domain.ErrReservationConflict):
		outcome = "conflict"
	case errors.Is(err, domain.ErrReservationCoordinationUnavailable):
		outcome = "coordination_failed"
	}
	metrics.ReservationAttempts.WithLabelValues(outcome).Inc()
	metrics.ReservationDuration.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
	logger.L(ctx).Warn("reservation rejected",
		zap.String("outcome", outcome),
		zap.String("order_id", orderID.String()),
		zap.Error(err),
	)
}

// fingerprintRequest hashes the canonical sorted (variant_id, quantity) pairs
// into a SHA-256 request fingerprint used for durable idempotency.
func fingerprintRequest(items []domain.ReservationRequestItem) []byte {
	h := sha256.New()
	var qbuf [8]byte
	for _, it := range items {
		h.Write(it.VariantID[:])
		binary.BigEndian.PutUint64(qbuf[:], uint64(it.Quantity))
		h.Write(qbuf[:])
	}
	return h.Sum(nil)
}

func uuidLess(a, b uuid.UUID) bool {
	return bytes.Compare(a[:], b[:]) < 0
}
