package handler

import (
	"fmt"
	"net/http"

	"gin-product-service/internal/delivery/http/dto"
	"gin-product-service/internal/domain"

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
		fmt.Println("errorrr heee >>>>>>>>>>>>>>>>> xxxxx")
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}
	if err := validate.Struct(req); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {

			fmt.Println("errorrr heee >>>>>>>>>>>>>>>>>")
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
// @Description  Reserve multiple variants for an order atomically; retrying the same order_id returns the existing reservations (idempotent)
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

// mapInventoryError maps domain errors to HTTP status codes per spec
// Section 21 (400/404/409/500).
func mapInventoryError(err error) (int, string) {
	switch err {
	case domain.ErrInvalidStockMoveInput,
		domain.ErrInvalidReservationInput,
		domain.ErrInvalidQuantity:
		return http.StatusBadRequest, "ERR_VALIDATION"
	case domain.ErrFromToLocationSame:
		return http.StatusBadRequest, "ERR_SAME_LOCATION"
	case domain.ErrVariantNotFound:
		return http.StatusNotFound, "ERR_VARIANT_NOT_FOUND"
	case domain.ErrInventoryItemNotFound:
		return http.StatusNotFound, "ERR_INVENTORY_ITEM_NOT_FOUND"
	case domain.ErrLocationNotFound:
		return http.StatusNotFound, "ERR_LOCATION_NOT_FOUND"
	case domain.ErrInventoryLevelNotFound:
		return http.StatusNotFound, "ERR_INVENTORY_LEVEL_NOT_FOUND"
	case domain.ErrReservationNotFound:
		return http.StatusNotFound, "ERR_RESERVATION_NOT_FOUND"
	case domain.ErrInsufficientStock:
		return http.StatusConflict, "ERR_INSUFFICIENT_STOCK"
	case domain.ErrReservationAlreadyCompleted:
		return http.StatusConflict, "ERR_RESERVATION_ALREADY_COMPLETED"
	case domain.ErrReservationAlreadyCancelled:
		return http.StatusConflict, "ERR_RESERVATION_ALREADY_CANCELLED"
	case domain.ErrDuplicateActiveReservation:
		return http.StatusConflict, "ERR_DUPLICATE_ACTIVE_RESERVATION"
	default:
		return http.StatusInternalServerError, "ERR_INTERNAL"
	}
}
