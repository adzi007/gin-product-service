package handler

import (
	"gin-product-service/internal/domain"
	"net/http"

	"github.com/gin-gonic/gin"
)

type CategoryHandler struct {
	// useCase domain.CategoryUseCase
	insertUseCase domain.QueryCategoryUseCase
}

func NewCategoryHandler(insertUseCase domain.QueryCategoryUseCase) *CategoryHandler {
	return &CategoryHandler{
		insertUseCase: insertUseCase,
	}
}

// Fetch godoc
// @Summary      List all categories
// @Description  Get a list of all existing categories
// @Tags         categories
// @Produce      json
// @Success      200  {array}   domain.Category
// @Failure      500  {object}  map[string]string
// @Router       /categories [get]
func (h *CategoryHandler) Fetch(c *gin.Context) {

	ctx := c.Request.Context()

	data, err := h.insertUseCase.FindAll(ctx)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return // Return immediately after error
	}

	c.JSON(http.StatusOK, data)
}
