package handler

import (
	"gin-product-service/internal/domain"
	"net/http"

	"github.com/gin-gonic/gin"
)

type CategoryHandler struct {
	// useCase domain.CategoryUseCase
}

func NewCategoryHandler() *CategoryHandler {
	return &CategoryHandler{}
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

	// res, err := h.useCase.Fetch(c.Request.Context())
	// if err != nil {
	// 	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	// 	return
	// }

	mesage := domain.Category{
		Pesan: "hello world",
	}
	c.JSON(http.StatusOK, mesage)
}
