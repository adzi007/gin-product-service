package handler

import (
	"errors"
	"net/http"
	"time"

	"gin-product-service/internal/delivery/http/dto"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/metrics"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// InventoryHandler exposes the inventory delivery endpoints.
type InventoryHandler struct {
	createReservationUC domain.CreateReservationUseCase
}

// NewInventoryHandler constructs the inventory handler.
func NewInventoryHandler(createReservationUC domain.CreateReservationUseCase) *InventoryHandler {
	return &InventoryHandler{createReservationUC: createReservationUC}
}

// CreateReservation godoc
// @Summary      Create an atomic checkout inventory reservation
// @Description  Reserve sufficient stock for one order at the default fulfillment location. Idempotent by orderId; the order service supplies the hold expiry.
// @Tags         inventory
// @Accept       json
// @Produce      json
// @Param        body body dto.CreateReservationRequest true "Checkout reservation request"
// @Success      201  {object} dto.ReservationSuccessResponse
// @Success      200  {object} dto.ReservationSuccessResponse
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      422  {object} map[string]any
// @Failure      503  {object} map[string]any
// @Router       /inventory/reservations [post]
func (h *InventoryHandler) CreateReservation(c *gin.Context) {
	ctx := c.Request.Context()
	start := time.Now()

	var input dto.CreateReservationRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		h.respondBindingError(c, err, start)
		return
	}
	if err := validate.Struct(input); err != nil {
		h.respondBindingError(c, err, start)
		return
	}

	orderID, expiresAt, items, err := input.ToDomain()
	if err != nil {
		if errors.Is(err, domain.ErrInvalidExpiry) {
			recordExpiryInvalid(start, time.Now())
			c.JSON(http.StatusBadRequest, reservationErrorResponse("ERR_INVALID_EXPIRY", domain.ErrInvalidExpiry.Error(), nil))
			return
		}
		c.JSON(http.StatusBadRequest, reservationErrorResponse("ERR_VALIDATION", "orderId and item ids must be valid UUIDs", nil))
		return
	}

	result, err := h.createReservationUC.Create(ctx, orderID, expiresAt, items)
	if err != nil {
		status, code, message, details := mapReservationError(err)
		c.JSON(status, reservationErrorResponse(code, message, details))
		return
	}

	status := http.StatusCreated
	if result.Retried {
		status = http.StatusOK
	}
	c.JSON(status, dto.ToReservationResponse(result))
}

// respondBindingError maps JSON binding/validation failures to the documented
// response. A missing expiresAt is reported as ERR_INVALID_EXPIRY rather than
// a generic validation failure so consumers can distinguish the two.
func (h *InventoryHandler) respondBindingError(c *gin.Context, err error, start time.Time) {
	if expiryBindingFailed(err) {
		recordExpiryInvalid(start, time.Now())
		c.JSON(http.StatusBadRequest, reservationErrorResponse("ERR_INVALID_EXPIRY", domain.ErrInvalidExpiry.Error(), nil))
		return
	}
	c.JSON(http.StatusBadRequest, reservationErrorResponse("ERR_VALIDATION", reservationValidationMessage(err), nil))
}

// expiryBindingFailed reports whether err is a validator error concerning the
// required expiresAt field. Gin's binding validator reports the JSON tag name
// while the standalone validator reports the Go field name, so both are
// matched.
func expiryBindingFailed(err error) bool {
	fieldErrs, ok := err.(validator.ValidationErrors)
	if !ok {
		return false
	}
	for _, fe := range fieldErrs {
		if fe.Field() == "ExpiresAt" || fe.Field() == "expiresAt" {
			return true
		}
	}
	return false
}

// recordExpiryInvalid emits the invalid-expiry outcome metric with a latency
// observation consistent with the use case's other outcomes.
func recordExpiryInvalid(start, end time.Time) {
	metrics.ReservationAttempts.WithLabelValues("expiry_invalid").Inc()
	metrics.ReservationDuration.WithLabelValues("expiry_invalid").Observe(end.Sub(start).Seconds())
}

// reservationErrorResponse builds the reservation spec error envelope. details
// is omitted unless item-level context (the public variantId) is present.
func reservationErrorResponse(code, message string, details gin.H) gin.H {
	resp := gin.H{
		"status":  "error",
		"code":    code,
		"message": message,
	}
	if len(details) > 0 {
		resp["details"] = details
	}
	return resp
}

// reservationValidationMessage flattens validator errors into a single message.
func reservationValidationMessage(err error) string {
	if fieldErrs, ok := err.(validator.ValidationErrors); ok && len(fieldErrs) > 0 {
		return fieldValidationMessage(fieldErrs[0])
	}
	return err.Error()
}

// mapReservationError maps reservation domain errors to the documented HTTP
// status, machine-readable code, safe message, and (for item-level failures)
// the public failing variantId.
func mapReservationError(err error) (int, string, string, gin.H) {
	var ve *domain.VariantError
	if errors.As(err, &ve) {
		status, code, message := mapReservationItemError(ve.Err)
		return status, code, message, gin.H{"variantId": ve.VariantID.String()}
	}

	switch err {
	case domain.ErrReservationValidation:
		return http.StatusBadRequest, "ERR_VALIDATION", err.Error(), nil
	case domain.ErrInvalidExpiry:
		return http.StatusBadRequest, "ERR_INVALID_EXPIRY", err.Error(), nil
	case domain.ErrExpiredExpiry:
		return http.StatusUnprocessableEntity, "ERR_EXPIRED_EXPIRY", err.Error(), nil
	case domain.ErrReservationInventoryNotFound:
		return http.StatusNotFound, "ERR_INVENTORY_NOT_FOUND", err.Error(), nil
	case domain.ErrReservationConflict:
		return http.StatusConflict, "ERR_RESERVATION_CONFLICT", err.Error(), nil
	case domain.ErrReservationCoordinationUnavailable:
		return http.StatusServiceUnavailable, "ERR_COORDINATION_UNAVAILABLE", err.Error(), nil
	default:
		return http.StatusInternalServerError, "ERR_INTERNAL", err.Error(), nil
	}
}

// mapReservationItemError maps the stable inner error of a VariantError.
func mapReservationItemError(err error) (int, string, string) {
	switch err {
	case domain.ErrInsufficientStock:
		return http.StatusUnprocessableEntity, "ERR_INSUFFICIENT_STOCK", "requested quantity exceeds available stock"
	case domain.ErrReservationVariantNotFound:
		return http.StatusNotFound, "ERR_VARIANT_NOT_FOUND", err.Error()
	case domain.ErrVariantNotReservable:
		return http.StatusConflict, "ERR_VARIANT_NOT_RESERVABLE", err.Error()
	case domain.ErrReservationInventoryNotFound:
		return http.StatusNotFound, "ERR_INVENTORY_NOT_FOUND", err.Error()
	default:
		return http.StatusInternalServerError, "ERR_INTERNAL", err.Error()
	}
}
