package handler

import (
	"errors"
	"net/http"

	"gin-product-service/internal/delivery/http/dto"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

// InventoryHandler exposes the service-to-service inventory APIs (spec
// Section 1). It is not intended for frontend use.
type InventoryHandler struct {
	stockMoveUseCase   domain.StockMoveUseCase
	reservationUseCase domain.ReservationUseCase
}

func NewInventoryHandler(
	stockMoveUseCase domain.StockMoveUseCase,
	reservationUseCase domain.ReservationUseCase,
) *InventoryHandler {
	return &InventoryHandler{
		stockMoveUseCase:   stockMoveUseCase,
		reservationUseCase: reservationUseCase,
	}
}

// CreateStockMove godoc
// @Summary      Create a stock move
// @Description  Record a physical/administrative stock movement and update the affected inventory level(s) atomically (IN/OUT/TRANSFER/ADJUST)
// @Tags         inventory
// @Accept       json
// @Produce      json
// @Param        body body dto.CreateStockMoveRequest true "Stock move to create"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /inventory/stock-moves [post]
func (h *InventoryHandler) CreateStockMove(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.CreateStockMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {

		msg, details := utils.ParseBindingError(err)
		status, code := mapProductError(err)

		c.JSON(status, errorBadRequestResponse(code, msg, details))
		return

		// c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		// return
	}
	if err := validate.Struct(req); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {

			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"code":    "ERR_VALIDATION",
				"message": "validation failed",
				"details": validationDetails(fieldErrs),
			})
			return
		}
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	move, err := h.stockMoveUseCase.Create(ctx, req.ToDomain())
	if err != nil {
		status, code := mapInventoryError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   dto.ToStockMoveData(move, req.VariantID),
	})
}

// CreateReservation godoc
// @Summary      Create reservations for an order
// @Description  Reserve multiple variants for an order atomically; retrying the same order_id returns the existing reservations (idempotent). The location to reserve from is chosen automatically for each item.
// @Tags         inventory
// @Accept       json
// @Produce      json
// @Param        body body dto.CreateReservationRequest true "Reservations to create"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /inventory/reservations [post]
func (h *InventoryHandler) CreateReservation(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.CreateReservationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}
	if err := validate.Struct(req); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"code":    "ERR_VALIDATION",
				"message": "validation failed",
				"details": validationDetails(fieldErrs),
			})
			return
		}
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	result, err := h.reservationUseCase.Create(ctx, req.ToDomain())
	if err != nil {
		status, code := mapInventoryError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   dto.ToReservationResultData(result),
	})
}

// CompleteReservation godoc
// @Summary      Complete an order's reservations
// @Description  Transition all ACTIVE reservations of an order to COMPLETED (consumes reserved stock). Idempotent.
// @Tags         inventory
// @Produce      json
// @Param        orderId path string true "Order ID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /inventory/reservations/{orderId}/complete [put]
func (h *InventoryHandler) CompleteReservation(c *gin.Context) {
	ctx := c.Request.Context()

	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", "invalid order id"))
		return
	}

	result, err := h.reservationUseCase.Complete(ctx, orderID)
	if err != nil {
		status, code := mapInventoryError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   dto.ToReservationResultData(result),
	})
}

// CancelReservation godoc
// @Summary      Cancel an order's reservations
// @Description  Transition all ACTIVE reservations of an order to CANCELLED (returns reserved stock to available). Idempotent.
// @Tags         inventory
// @Produce      json
// @Param        orderId path string true "Order ID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /inventory/reservations/{orderId}/cancel [put]
func (h *InventoryHandler) CancelReservation(c *gin.Context) {
	ctx := c.Request.Context()

	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", "invalid order id"))
		return
	}

	result, err := h.reservationUseCase.Cancel(ctx, orderID)
	if err != nil {
		status, code := mapInventoryError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   dto.ToReservationResultData(result),
	})
}

// validationDetails renders validator errors as human-readable messages.
func validationDetails(fieldErrs validator.ValidationErrors) []string {
	details := make([]string, 0, len(fieldErrs))
	for _, fe := range fieldErrs {
		details = append(details, fieldValidationMessage(fe))
	}
	return details
}

func errorBadRequestResponse(code, message string, details []utils.CustomFieldError) gin.H {
	return gin.H{
		"status":  "error",
		"code":    code,
		"message": message,
		"details": details,
	}
}

// mapInventoryError maps domain errors to HTTP status codes per spec
// Section 21 (400/404/409/500).
func mapInventoryError(err error) (int, string) {
	switch {
	// 400 Bad Request: Business Validation Errors
	case errors.Is(err, domain.ErrInvalidStockMoveInput),
		errors.Is(err, domain.ErrInvalidReservationInput),
		errors.Is(err, domain.ErrInvalidQuantity),
		errors.Is(err, domain.ErrVariantIDRequired),
		errors.Is(err, domain.ErrToLocationRequired),
		errors.Is(err, domain.ErrFromLocationRequired),
		errors.Is(err, domain.ErrInvalidStockMoveType):
		return http.StatusBadRequest, "ERR_VALIDATION"

	case errors.Is(err, domain.ErrFromToLocationSame):
		return http.StatusBadRequest, "ERR_SAME_LOCATION"

	// 404 Not Found
	case errors.Is(err, domain.ErrVariantNotFound):
		return http.StatusNotFound, "ERR_VARIANT_NOT_FOUND"
	case errors.Is(err, domain.ErrInventoryItemNotFound):
		return http.StatusNotFound, "ERR_INVENTORY_ITEM_NOT_FOUND"
	case errors.Is(err, domain.ErrLocationNotFound):
		return http.StatusNotFound, "ERR_LOCATION_NOT_FOUND"
	case errors.Is(err, domain.ErrInventoryLevelNotFound):
		return http.StatusNotFound, "ERR_INVENTORY_LEVEL_NOT_FOUND"
	case errors.Is(err, domain.ErrReservationNotFound):
		return http.StatusNotFound, "ERR_RESERVATION_NOT_FOUND"

	// 409 Conflict
	case errors.Is(err, domain.ErrInsufficientStock):
		return http.StatusConflict, "ERR_INSUFFICIENT_STOCK"
	case errors.Is(err, domain.ErrReservationAlreadyCompleted):
		return http.StatusConflict, "ERR_RESERVATION_ALREADY_COMPLETED"
	case errors.Is(err, domain.ErrReservationAlreadyCancelled):
		return http.StatusConflict, "ERR_RESERVATION_ALREADY_CANCELLED"
	case errors.Is(err, domain.ErrDuplicateActiveReservation):
		return http.StatusConflict, "ERR_DUPLICATE_ACTIVE_RESERVATION"

	// 500 Internal Server Error
	default:
		return http.StatusInternalServerError, "ERR_INTERNAL"
	}
}
