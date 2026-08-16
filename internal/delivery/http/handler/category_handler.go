package handler

import (
	"gin-product-service/internal/domain"
	"net/http"

	"github.com/gin-gonic/gin"
)

type CategoryHandler struct {
	// useCase domain.CategoryUseCase
	queryUseCase domain.QueryCategoryUseCase
}

func NewCategoryHandler(queryUseCase domain.QueryCategoryUseCase) *CategoryHandler {
	return &CategoryHandler{
		queryUseCase: queryUseCase,
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

	data, err := h.queryUseCase.FindAll(ctx)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return // Return immediately after error
	}

	type ReponseObject struct {
		Message string      `json:"message"`
		Data    interface{} `json:"data"`
	}

	res := ReponseObject{
		Message: "berhasil lalalal yeyeyeye",
		Data:    data,
	}

	c.JSON(http.StatusOK, res)
}
