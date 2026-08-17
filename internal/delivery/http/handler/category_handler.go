package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// validate is a shared validator instance used to validate request payloads.
var validate = validator.New()

type CategoryHandler struct {
	queryUseCase  domain.QueryCategoryUseCase
	insertUseCase domain.InsertCategoryUseCase
	updateUseCase domain.UpdateCategoryUseCase
	deleteUseCase domain.DeleteCategoryUseCase
}

func NewCategoryHandler(
	queryUseCase domain.QueryCategoryUseCase,
	insertUseCase domain.InsertCategoryUseCase,
	updateUseCase domain.UpdateCategoryUseCase,
	deleteUseCase domain.DeleteCategoryUseCase,
) *CategoryHandler {
	return &CategoryHandler{
		queryUseCase:  queryUseCase,
		insertUseCase: insertUseCase,
		updateUseCase: updateUseCase,
		deleteUseCase: deleteUseCase,
	}
}

// Fetch godoc
// @Summary      List all categories
// @Description  Get a paginated, filterable list of all existing categories
// @Tags         categories
// @Produce      json
// @Param        name     query string false "Filter by partial (case-insensitive) name match"
// @Param        page     query int    false "Page number (1-indexed)"
// @Param        per_page query int    false "Items per page"
// @Param        sort_by  query string false "Sort column. One of: name, created_at"
// @Param        sort_dir query string false "Sort direction. One of: asc, desc"
// @Success      200      {object} domain.PaginatedCategories
// @Failure      400      {object} map[string]string
// @Failure      500      {object} map[string]string
// @Router       /categories [get]
func (h *CategoryHandler) Fetch(c *gin.Context) {

	ctx := c.Request.Context()

	page, _ := strconv.Atoi(c.Query("page"))
	perPage, _ := strconv.Atoi(c.Query("per_page"))

	sortBy := strings.ToLower(c.Query("sort_by"))
	if sortBy == "" {
		sortBy = "name"
	}
	if sortBy != "name" && sortBy != "created_at" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort_by: must be one of name, created_at"})
		return
	}

	sortDir := strings.ToLower(c.Query("sort_dir"))
	if sortDir == "" {
		sortDir = "asc"
	}
	if sortDir != "asc" && sortDir != "desc" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort_dir: must be one of asc, desc"})
		return
	}

	params := domain.ListCategoryParams{
		Name:    c.Query("name"),
		Page:    page,
		PerPage: perPage,
		SortBy:  sortBy,
		SortDir: sortDir,
	}

	data, err := h.queryUseCase.FindAll(ctx, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type ResponseObject struct {
		Message string                     `json:"message"`
		Data    domain.PaginatedCategories `json:"data"`
	}

	res := ResponseObject{
		Message: "success",
		Data:    data,
	}

	c.JSON(http.StatusOK, res)
}

// Dropdown godoc
// @Summary      List categories for dropdown
// @Description  Get a lightweight list (id + name) of categories for dropdown UI
// @Tags         categories
// @Produce      json
// @Param        name query string false "Filter by partial (case-insensitive) name match"
// @Success      200  {array} domain.CategoryOption
// @Failure      500  {object} map[string]string
// @Router       /categories/dropdown [get]
func (h *CategoryHandler) Dropdown(c *gin.Context) {

	ctx := c.Request.Context()

	data, err := h.queryUseCase.FindAllForDropdown(ctx, c.Query("name"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type ResponseObject struct {
		Message string                  `json:"message"`
		Data    []domain.CategoryOption `json:"data"`
	}

	res := ResponseObject{
		Message: "success",
		Data:    data,
	}

	c.JSON(http.StatusOK, res)
}

// GetByID godoc
// @Summary      Get a category by ID
// @Description  Get a single category by its numeric ID
// @Tags         categories
// @Produce      json
// @Param        id path int true "Category ID"
// @Success      200  {object} domain.Category
// @Failure      400  {object} map[string]string
// @Failure      404  {object} map[string]string
// @Failure      500  {object} map[string]string
// @Router       /categories/{id} [get]
func (h *CategoryHandler) GetByID(c *gin.Context) {

	ctx := c.Request.Context()

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category id"})
		return
	}

	data, err := h.queryUseCase.GetByID(ctx, id)
	if err != nil {
		if err == domain.ErrCategoryNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, data)
}

// Create godoc
// @Summary      Create a category
// @Description  Create a new product category
// @Tags         categories
// @Accept       json
// @Produce      json
// @Param        body body domain.CreateCategoryInput true "Category to create"
// @Success      201  {object} domain.Category
// @Failure      400  {object} map[string]any
// @Failure      500  {object} map[string]string
// @Router       /categories [post]
func (h *CategoryHandler) Create(c *gin.Context) {

	ctx := c.Request.Context()

	var input domain.CreateCategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(input); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {
			details := make([]string, 0, len(fieldErrs))
			for _, fe := range fieldErrs {
				details = append(details, fieldValidationMessage(fe))
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed", "details": details})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	created, err := h.insertUseCase.Create(ctx, input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, created)
}

// Update godoc
// @Summary      Update a category
// @Description  Update an existing product category
// @Tags         categories
// @Accept       json
// @Produce      json
// @Param        id   path int  true "Category ID"
// @Param        body body domain.UpdateCategoryInput true "Updated category data"
// @Success      200  {object} domain.Category
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]string
// @Failure      500  {object} map[string]string
// @Router       /categories/{id} [put]
func (h *CategoryHandler) Update(c *gin.Context) {

	ctx := c.Request.Context()

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category id"})
		return
	}

	var input domain.UpdateCategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(input); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {
			details := make([]string, 0, len(fieldErrs))
			for _, fe := range fieldErrs {
				details = append(details, fieldValidationMessage(fe))
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed", "details": details})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.updateUseCase.Update(ctx, id, input)
	if err != nil {
		if err == domain.ErrCategoryNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, updated)
}

// Delete godoc
// @Summary      Delete a category (soft delete)
// @Description  Soft-delete an existing product category
// @Tags         categories
// @Produce      json
// @Param        id path int true "Category ID"
// @Success      204
// @Failure      400  {object} map[string]string
// @Failure      404  {object} map[string]string
// @Failure      500  {object} map[string]string
// @Router       /categories/{id} [delete]
func (h *CategoryHandler) Delete(c *gin.Context) {

	ctx := c.Request.Context()

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category id"})
		return
	}

	err = h.deleteUseCase.Delete(ctx, id)
	if err != nil {
		if err == domain.ErrCategoryNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// fieldValidationMessage returns a human-readable message for a validator error.
func fieldValidationMessage(e validator.FieldError) string {
	switch e.Tag() {
	case "required":
		return e.Field() + " is required"
	case "min":
		return fmt.Sprintf("%s must be at least %s", e.Field(), e.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", e.Field(), e.Param())
	default:
		return fmt.Sprintf("%s failed validation on %s", e.Field(), e.Tag())
	}
}
