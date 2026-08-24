package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gin-product-service/internal/delivery/http/dto"
	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ProductHandler struct {
	insertUseCase  domain.InsertProductUseCase
	queryUseCase   domain.QueryProductUseCase
	updateUseCase  domain.UpdateProductUseCase
	deleteUseCase  domain.DeleteProductUseCase
	optionUseCase  domain.OptionUseCase
	variantUseCase domain.VariantUseCase
	mediaUseCase   domain.MediaUseCase
}

func NewProductHandler(
	insertUseCase domain.InsertProductUseCase,
	queryUseCase domain.QueryProductUseCase,
	updateUseCase domain.UpdateProductUseCase,
	deleteUseCase domain.DeleteProductUseCase,
	optionUseCase domain.OptionUseCase,
	variantUseCase domain.VariantUseCase,
	mediaUseCase domain.MediaUseCase,
) *ProductHandler {
	return &ProductHandler{
		insertUseCase:  insertUseCase,
		queryUseCase:   queryUseCase,
		updateUseCase:  updateUseCase,
		deleteUseCase:  deleteUseCase,
		optionUseCase:  optionUseCase,
		variantUseCase: variantUseCase,
		mediaUseCase:   mediaUseCase,
	}
}

// Create godoc
// @Summary      Create a product
// @Description  Create a product with options, variants, gallery and initial stock in one transaction
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        body body dto.CreateProductInput true "Product to create"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products [post]
func (h *ProductHandler) Create(c *gin.Context) {

	ctx := c.Request.Context()

	var input dto.CreateProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {
			details := make([]string, 0, len(fieldErrs))
			for _, fe := range fieldErrs {
				details = append(details, fieldValidationMessage(fe))
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"code":    "ERR_VALIDATION",
				"message": "validation failed",
				"details": details,
			})
			return
		}
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	created, err := h.insertUseCase.Create(ctx, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   toProductData(created),
	})
}

// Fetch godoc
// @Summary      List products
// @Description  Get a paginated, filterable list of products
// @Tags         products
// @Produce      json
// @Param        search      query string false "Filter by partial (case-insensitive) title, handle or category name match"
// @Param        category_id query int    false "Filter by category ID (0 means no filter)"
// @Param        status      query string false "Filter by status. One of: draft, active, archived"
// @Param        minPrice    query number false "Minimum variant price (inclusive). Products must have a variant priced >= minPrice"
// @Param        maxPrice    query number false "Maximum variant price (inclusive). Products must have a variant priced <= maxPrice"
// @Param        page        query int    false "Page number (1-indexed)"
// @Param        per_page    query int    false "Items per page"
// @Param        sort_by     query string false "Sort column. One of: title, created_at, category_name"
// @Param        sort_dir    query string false "Sort direction. One of: asc, desc"
// @Success      200      {object} domain.PaginatedProducts
// @Failure      400      {object} map[string]any
// @Failure      500      {object} map[string]any
// @Router       /products [get]
func (h *ProductHandler) Fetch(c *gin.Context) {

	ctx := c.Request.Context()

	page, _ := strconv.Atoi(c.Query("page"))
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	categoryID, _ := strconv.Atoi(c.Query("category_id"))

	status := strings.ToLower(c.Query("status"))
	if status != "" && status != "draft" && status != "active" && status != "archived" {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", "invalid status: must be one of draft, active, archived"))
		return
	}

	sortBy := strings.ToLower(c.Query("sort_by"))
	if sortBy == "" {
		sortBy = "created_at"
	}
	if sortBy != "title" && sortBy != "created_at" && sortBy != "category_name" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort_by: must be one of title, created_at, category_name"})
		return
	}

	sortDir := strings.ToLower(c.Query("sort_dir"))
	if sortDir == "" {
		sortDir = "desc"
	}
	if sortDir != "asc" && sortDir != "desc" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort_dir: must be one of asc, desc"})
		return
	}

	minPrice, err := parsePriceQueryParam(c, "minPrice")
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}
	maxPrice, err := parsePriceQueryParam(c, "maxPrice")
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}
	if minPrice != nil && maxPrice != nil && minPrice.GreaterThan(*maxPrice) {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", "invalid price range: minPrice must not be greater than maxPrice"))
		return
	}

	params := domain.ListProductParams{
		Search:     c.Query("search"),
		CategoryID: categoryID,
		Status:     status,
		MinPrice:   minPrice,
		MaxPrice:   maxPrice,
		Page:       page,
		PerPage:    perPage,
		SortBy:     sortBy,
		SortDir:    sortDir,
	}

	data, err := h.queryUseCase.FindAll(ctx, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse("ERR_INTERNAL", err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   data,
	})
}

// parsePriceQueryParam parses an optional decimal query parameter used by the
// price filter. An empty value means "not provided" and returns a nil pointer.
// A non-empty value must parse as a valid non-negative decimal number; both
// `decimal.NewFromString` failures (e.g. "abc", "NaN") and negative values are
// rejected.
func parsePriceQueryParam(c *gin.Context, name string) (*decimal.Decimal, error) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: must be a valid non-negative number", name)
	}
	if value.IsNegative() {
		return nil, fmt.Errorf("invalid %s: must not be negative", name)
	}
	return &value, nil
}

// GetByID godoc
// @Summary      Get a product by ID or handle
// @Description  Get a single product (with options, variants and media) by UUID or handle
// @Tags         products
// @Produce      json
// @Param        id path string true "Product UUID or handle"
// @Success      200  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id} [get]
func (h *ProductHandler) GetByID(c *gin.Context) {

	ctx := c.Request.Context()

	param := c.Param("id")

	// A valid UUID is resolved by ID; anything else is treated as a handle,
	// so this single route serves both /products/:id and /products/:handle.
	if id, err := uuid.Parse(param); err == nil {
		product, err := h.queryUseCase.GetByID(ctx, id)
		if err != nil {
			status, code := mapProductError(err)
			c.JSON(status, errorResponse(code, err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   toProductDetailData(product),
		})
		return
	}

	product, err := h.queryUseCase.GetByHandle(ctx, param)
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toProductDetailData(product),
	})
}

// Update godoc
// @Summary      Update a product
// @Description  Update product header fields only (title, description, vendor, handle, categoryId, status). Omitted fields are left unchanged.
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.UpdateProductInput true "Fields to update"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id} [patch]
func (h *ProductHandler) Update(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.UpdateProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {
			details := make([]string, 0, len(fieldErrs))
			for _, fe := range fieldErrs {
				details = append(details, fieldValidationMessage(fe))
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"code":    "ERR_VALIDATION",
				"message": "validation failed",
				"details": details,
			})
			return
		}
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	updated, err := h.updateUseCase.Update(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toProductData(updated),
	})
}

// Archive godoc
// @Summary      Archive a product (soft delete)
// @Description  Soft-delete a product by setting its status to archived
// @Tags         products
// @Produce      json
// @Param        id path string true "Product UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id} [delete]
func (h *ProductHandler) Archive(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if err := h.updateUseCase.Archive(ctx, id); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Product archived",
	})
}

// Restore godoc
// @Summary      Restore an archived product
// @Description  Un-archive a product by setting its status back to active
// @Tags         products
// @Produce      json
// @Param        id path string true "Product UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/restore [post]
func (h *ProductHandler) Restore(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if err := h.updateUseCase.Restore(ctx, id); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Product restored",
	})
}

// Purge godoc
// @Summary      Permanently delete a product
// @Description  Hard-delete a product and its related rows. Blocked (409) when the product has stock movement history.
// @Tags         products
// @Produce      json
// @Param        id path string true "Product UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/purge [delete]
func (h *ProductHandler) Purge(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if err := h.deleteUseCase.Purge(ctx, id); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Product permanently deleted",
	})
}

// CreateOption godoc
// @Summary      Create a product option
// @Description  Create a new option with its initial values for a product
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.CreateOptionInput true "Option to create"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/options [post]
func (h *ProductHandler) CreateOption(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.CreateOptionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {
			details := make([]string, 0, len(fieldErrs))
			for _, fe := range fieldErrs {
				details = append(details, fieldValidationMessage(fe))
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"code":    "ERR_VALIDATION",
				"message": "validation failed",
				"details": details,
			})
			return
		}
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	created, err := h.optionUseCase.Create(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   toProductOptionData(created),
	})
}

// RenameOption godoc
// @Summary      Rename a product option
// @Description  Rename an existing option on a product
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id        path string true "Product UUID"
// @Param        option_id path string true "Option UUID"
// @Param        body      body dto.UpdateOptionInput true "New option name"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/options/{option_id} [patch]
func (h *ProductHandler) RenameOption(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	optionID, ok := parseUUID(c, "option_id")
	if !ok {
		return
	}

	var input dto.UpdateOptionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	renamed, err := h.optionUseCase.Rename(ctx, id, optionID, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toProductOptionData(renamed),
	})
}

// DeleteOption godoc
// @Summary      Delete a product option
// @Description  Remove an option and all of its values from a product
// @Tags         products
// @Produce      json
// @Param        id        path string true "Product UUID"
// @Param        option_id path string true "Option UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/options/{option_id} [delete]
func (h *ProductHandler) DeleteOption(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	optionID, ok := parseUUID(c, "option_id")
	if !ok {
		return
	}

	if err := h.optionUseCase.Delete(ctx, id, optionID); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Option deleted",
	})
}

// ReorderOptions godoc
// @Summary      Reorder product options
// @Description  Bulk-update the positions of a product's options
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.ReorderOptionsInput true "New positions"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/options/reorder [patch]
func (h *ProductHandler) ReorderOptions(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.ReorderOptionsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := h.optionUseCase.Reorder(ctx, id, input.ToDomain()); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Options reordered",
	})
}

// AddOptionValue godoc
// @Summary      Add an option value
// @Description  Add a new value to an existing option
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id        path string true "Product UUID"
// @Param        option_id path string true "Option UUID"
// @Param        body      body dto.CreateOptionValueInput true "Value to add"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/options/{option_id}/values [post]
func (h *ProductHandler) AddOptionValue(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	optionID, ok := parseUUID(c, "option_id")
	if !ok {
		return
	}

	var input dto.CreateOptionValueInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	created, err := h.optionUseCase.AddValue(ctx, id, optionID, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   toProductOptionValueData(created),
	})
}

// UpdateOptionValue godoc
// @Summary      Update an option value
// @Description  Rename or reorder a single option value
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id        path string true "Product UUID"
// @Param        option_id path string true "Option UUID"
// @Param        value_id  path string true "Option value UUID"
// @Param        body      body dto.UpdateOptionValueInput true "Fields to update"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/options/{option_id}/values/{value_id} [patch]
func (h *ProductHandler) UpdateOptionValue(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	optionID, ok := parseUUID(c, "option_id")
	if !ok {
		return
	}
	valueID, ok := parseUUID(c, "value_id")
	if !ok {
		return
	}

	var input dto.UpdateOptionValueInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	updated, err := h.optionUseCase.UpdateValue(ctx, id, optionID, valueID, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toProductOptionValueData(updated),
	})
}

// DeleteOptionValue godoc
// @Summary      Delete an option value
// @Description  Remove a single value from an option
// @Tags         products
// @Produce      json
// @Param        id        path string true "Product UUID"
// @Param        option_id path string true "Option UUID"
// @Param        value_id  path string true "Option value UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/options/{option_id}/values/{value_id} [delete]
func (h *ProductHandler) DeleteOptionValue(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	optionID, ok := parseUUID(c, "option_id")
	if !ok {
		return
	}
	valueID, ok := parseUUID(c, "value_id")
	if !ok {
		return
	}

	if err := h.optionUseCase.DeleteValue(ctx, id, optionID, valueID); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Option value deleted",
	})
}

// CreateVariant godoc
// @Summary      Create a product variant
// @Description  Create a new variant for a product
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.CreateVariantInput true "Variant to create"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/variants [post]
func (h *ProductHandler) CreateVariant(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.CreateVariantInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	created, err := h.variantUseCase.Create(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   toVariantResponseData(created),
	})
}

// BulkCreateVariants godoc
// @Summary      Bulk create product variants
// @Description  Create multiple variants for a product (import/sync)
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.BulkCreateVariantsInput true "Variants to create"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/variants/bulk [post]
func (h *ProductHandler) BulkCreateVariants(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.BulkCreateVariantsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	created, err := h.variantUseCase.BulkCreate(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   toVariantResponseSlice(created),
	})
}

// UpdateVariant godoc
// @Summary      Update a variant
// @Description  Update variant fields (sku, barcode, title, price, weight). Omitted fields are left unchanged.
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Variant UUID"
// @Param        body body dto.UpdateVariantInput true "Fields to update"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /variants/{id} [patch]
func (h *ProductHandler) UpdateVariant(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.UpdateVariantInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	updated, err := h.variantUseCase.Update(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toVariantResponseData(updated),
	})
}

// BulkUpdateVariants godoc
// @Summary      Bulk update variants
// @Description  Update multiple variants of a product in a single request
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.BulkUpdateVariantsInput true "Variant updates"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/variants/bulk [patch]
func (h *ProductHandler) BulkUpdateVariants(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.BulkUpdateVariantsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	updated, err := h.variantUseCase.BulkUpdate(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toVariantResponseSlice(updated),
	})
}

// DeleteVariant godoc
// @Summary      Delete a variant
// @Description  Delete a variant (soft-delete if it has history, hard-delete otherwise)
// @Tags         products
// @Produce      json
// @Param        id path string true "Variant UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /variants/{id} [delete]
func (h *ProductHandler) DeleteVariant(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if err := h.variantUseCase.Delete(ctx, id); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Variant deleted",
	})
}

// BulkDeleteVariants godoc
// @Summary      Bulk delete variants
// @Description  Delete multiple variants of a product in a single request
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.BulkDeleteVariantsInput true "Variant IDs to delete"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/variants/bulk-delete [post]
func (h *ProductHandler) BulkDeleteVariants(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.BulkDeleteVariantsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := h.variantUseCase.BulkDelete(ctx, id, input.ToDomain()); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Variants deleted",
	})
}

// RestoreVariant godoc
// @Summary      Restore a soft-deleted variant
// @Description  Undo a soft delete on a variant
// @Tags         products
// @Produce      json
// @Param        id path string true "Variant UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /variants/{id}/restore [post]
func (h *ProductHandler) RestoreVariant(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	restored, err := h.variantUseCase.Restore(ctx, id)
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toVariantResponseData(restored),
	})
}

// ReorderVariants godoc
// @Summary      Reorder product variants
// @Description  Bulk-update the positions of a product's variants
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.ReorderVariantsInput true "New positions"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/variants/reorder [patch]
func (h *ProductHandler) ReorderVariants(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.ReorderVariantsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := h.variantUseCase.Reorder(ctx, id, input.ToDomainPositions()); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Variants reordered",
	})
}

// CreateMedia godoc
// @Summary      Attach media to a product gallery
// @Description  Upload/attach new media to a product's gallery (accepts an array)
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.BulkCreateMediaInput true "Media to create"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/media [post]
func (h *ProductHandler) CreateMedia(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.BulkCreateMediaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	created, err := h.mediaUseCase.Create(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   toMediaResponseSlice(created),
	})
}

// UpdateMedia godoc
// @Summary      Update media metadata
// @Description  Update a media item's metadata (alt_text)
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id       path string true "Product UUID"
// @Param        media_id path string true "Media UUID"
// @Param        body     body dto.UpdateMediaInput true "Fields to update"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/media/{media_id} [patch]
func (h *ProductHandler) UpdateMedia(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	mediaID, ok := parseUUID(c, "media_id")
	if !ok {
		return
	}

	var input dto.UpdateMediaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	updated, err := h.mediaUseCase.Update(ctx, id, mediaID, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   toMediaResponseData(updated),
	})
}

// DeleteMedia godoc
// @Summary      Delete product media
// @Description  Remove media from a product entirely (cascades variant media links)
// @Tags         products
// @Produce      json
// @Param        id       path string true "Product UUID"
// @Param        media_id path string true "Media UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/media/{media_id} [delete]
func (h *ProductHandler) DeleteMedia(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	mediaID, ok := parseUUID(c, "media_id")
	if !ok {
		return
	}

	if err := h.mediaUseCase.Delete(ctx, id, mediaID); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Media deleted",
	})
}

// ReorderMedia godoc
// @Summary      Reorder product media
// @Description  Bulk-update the positions of a product's gallery media
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.ReorderMediaInput true "New positions"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/media/reorder [patch]
func (h *ProductHandler) ReorderMedia(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.ReorderMediaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := h.mediaUseCase.Reorder(ctx, id, input.ToDomainPositions()); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Media reordered",
	})
}

// AttachVariantMedia godoc
// @Summary      Attach media to a variant
// @Description  Attach an existing product media item to a variant
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Variant UUID"
// @Param        body body dto.AttachVariantMediaInput true "Media to attach"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /variants/{id}/media [post]
func (h *ProductHandler) AttachVariantMedia(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.AttachVariantMediaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	link, err := h.mediaUseCase.AttachToVariant(ctx, id, input.ToDomain())
	if err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   toVariantMediaResponseData(link),
	})
}

// DetachVariantMedia godoc
// @Summary      Detach media from a variant
// @Description  Remove a media link from a variant only (media stays in the product gallery)
// @Tags         products
// @Produce      json
// @Param        id       path string true "Variant UUID"
// @Param        media_id path string true "Media UUID"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /variants/{id}/media/{media_id} [delete]
func (h *ProductHandler) DetachVariantMedia(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	mediaID, ok := parseUUID(c, "media_id")
	if !ok {
		return
	}

	if err := h.mediaUseCase.DetachFromVariant(ctx, id, mediaID); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Variant media detached",
	})
}

// ReorderVariantMedia godoc
// @Summary      Reorder variant media
// @Description  Bulk-update the positions of a variant's media subset
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        id   path string true "Variant UUID"
// @Param        body body dto.ReorderMediaInput true "New positions"
// @Success      200  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /variants/{id}/media/reorder [patch]
func (h *ProductHandler) ReorderVariantMedia(c *gin.Context) {

	ctx := c.Request.Context()

	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var input dto.ReorderMediaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := h.mediaUseCase.ReorderVariantMedia(ctx, id, input.ToDomainPositions()); err != nil {
		status, code := mapProductError(err)
		c.JSON(status, errorResponse(code, err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Variant media reordered",
	})
}

// parseUUID parses a path parameter as a UUID, writing a 400 ERR_VALIDATION
// response and returning false on failure.
func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("ERR_VALIDATION", "invalid "+param))
		return uuid.Nil, false
	}
	return id, true
}

// toProductOptionData converts a domain option to its response shape.
func toProductOptionData(opt domain.ProductOption) productOptionData {
	values := make([]productOptionValueData, 0, len(opt.Values))
	for _, v := range opt.Values {
		values = append(values, productOptionValueData{
			ID:       v.ID,
			Value:    v.Value,
			Position: v.Position,
		})
	}
	return productOptionData{
		ID:       opt.ID,
		Name:     opt.Name,
		Position: opt.Position,
		Values:   values,
	}
}

// toProductOptionValueData converts a domain option value to its response shape.
func toProductOptionValueData(v domain.ProductOptionValue) productOptionValueData {
	return productOptionValueData{
		ID:       v.ID,
		Value:    v.Value,
		Position: v.Position,
	}
}

func errorResponse(code, message string) gin.H {
	return gin.H{
		"status":  "error",
		"code":    code,
		"message": message,
	}
}

func mapProductError(err error) (int, string) {
	switch err {
	case domain.ErrSKUAlreadyExists:
		return http.StatusConflict, "ERR_SKU_ALREADY_EXISTS"
	case domain.ErrInvalidMediaAssignment:
		return http.StatusBadRequest, "ERR_INVALID_MEDIA_ASSIGNMENT"
	case domain.ErrInvalidOption:
		return http.StatusBadRequest, "ERR_INVALID_OPTION"
	case domain.ErrProductInvalidInput:
		return http.StatusBadRequest, "ERR_VALIDATION"
	case domain.ErrProductNotFound:
		return http.StatusNotFound, "ERR_PRODUCT_NOT_FOUND"
	case domain.ErrDefaultLocationNotFound:
		return http.StatusInternalServerError, "ERR_DEFAULT_LOCATION_NOT_FOUND"
	case domain.ErrOptionNotFound:
		return http.StatusNotFound, "ERR_OPTION_NOT_FOUND"
	case domain.ErrOptionValueNotFound:
		return http.StatusNotFound, "ERR_OPTION_VALUE_NOT_FOUND"
	case domain.ErrOptionAlreadyExists:
		return http.StatusConflict, "ERR_OPTION_ALREADY_EXISTS"
	case domain.ErrProductHandleAlreadyExists:
		return http.StatusConflict, "ERR_HANDLE_ALREADY_EXISTS"
	case domain.ErrProductHasHistory:
		return http.StatusConflict, "PRODUCT_HAS_HISTORY"
	case domain.ErrVariantNotFound:
		return http.StatusNotFound, "ERR_VARIANT_NOT_FOUND"
	case domain.ErrMediaNotFound:
		return http.StatusNotFound, "ERR_MEDIA_NOT_FOUND"
	case domain.ErrVariantMediaNotFound:
		return http.StatusNotFound, "ERR_VARIANT_MEDIA_NOT_FOUND"
	case domain.ErrProductInvalidStatusTransition:
		return http.StatusConflict, "ERR_INVALID_STATUS_TRANSITION"
	default:
		return http.StatusInternalServerError, "ERR_INTERNAL"
	}
}

type productData struct {
	ID         uuid.UUID            `json:"id"`
	Handle     string               `json:"handle"`
	Title      string               `json:"title"`
	Status     domain.ProductStatus `json:"status"`
	Vendor     *string              `json:"vendor,omitempty"`
	CategoryID int                  `json:"category_id"`
	Options    []productOptionData  `json:"options"`
	Variants   []variantData        `json:"variants"`
	CreatedAt  time.Time            `json:"created_at"`
}

type productOptionData struct {
	ID       uuid.UUID                `json:"id"`
	Name     string                   `json:"name"`
	Position int                      `json:"position"`
	Values   []productOptionValueData `json:"values"`
}

type productOptionValueData struct {
	ID       uuid.UUID `json:"id"`
	Value    string    `json:"value"`
	Position int       `json:"position"`
}

type variantData struct {
	ID      uuid.UUID              `json:"id"`
	SKU     *string                `json:"sku,omitempty"`
	Price   decimal.Decimal        `json:"price"`
	Options []domain.VariantOption `json:"options"`
	Media   []variantMediaData     `json:"media"`
}

// productCategoryData is the nested category shape exposed on the single
// product response. Mirrors domain.ProductCategory (slug + name only).
type productCategoryData struct {
	Id   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// productDetailData is the response shape for GET /products/:id_or_handle.
// Unlike productData (used by Create), it exposes a nested category object
// instead of category_id and adds per-variant stock.
type productDetailData struct {
	ID          uuid.UUID                `json:"id"`
	Handle      string                   `json:"handle"`
	Title       string                   `json:"title"`
	Status      domain.ProductStatus     `json:"status"`
	Description *string                  `json:"description,omitempty"`
	Vendor      *string                  `json:"vendor,omitempty"`
	Category    productCategoryData      `json:"category"`
	Options     []productOptionData      `json:"options"`
	Variants    []variantDetailData      `json:"variants"`
	CreatedAt   time.Time                `json:"created_at"`
	Galleries   []productGalleryItemData `json:"galleries"`
}

type variantDetailData struct {
	ID      uuid.UUID       `json:"id"`
	Title   string          `json:"title"`
	SKU     *string         `json:"sku,omitempty"`
	Price   decimal.Decimal `json:"price"`
	Weight  decimal.Decimal `json:"weight"`
	Barcode string          `json:"barcode"`
	// Stock   decimal.Decimal        `json:"stock"`
	Stock   int                    `json:"stock"`
	Options []domain.VariantOption `json:"options"`
	Media   []variantMediaData     `json:"media"`
}

type variantMediaData struct {
	ID       uuid.UUID `json:"id"`
	Position int       `json:"position"`
}

func toProductData(p domain.Product) productData {
	data := productData{
		ID:         p.ID,
		Handle:     p.Handle,
		Title:      p.Title,
		Status:     p.Status,
		Vendor:     p.Vendor,
		CategoryID: p.CategoryID,
		Options:    make([]productOptionData, 0, len(p.Options)),
		Variants:   make([]variantData, 0, len(p.Variants)),
		CreatedAt:  p.CreatedAt,
	}

	for _, opt := range p.Options {
		values := make([]productOptionValueData, 0, len(opt.Values))
		for _, value := range opt.Values {
			values = append(values, productOptionValueData{
				ID:       value.ID,
				Value:    value.Value,
				Position: value.Position,
			})
		}
		data.Options = append(data.Options, productOptionData{
			ID:       opt.ID,
			Name:     opt.Name,
			Position: opt.Position,
			Values:   values,
		})
	}

	for _, v := range p.Variants {
		var opts []domain.VariantOption
		_ = json.Unmarshal(v.Options, &opts)
		if opts == nil {
			opts = []domain.VariantOption{}
		}

		media := make([]variantMediaData, 0, len(v.Media))
		for _, m := range v.Media {
			media = append(media, variantMediaData{
				ID:       m.MediaID,
				Position: m.Position,
			})
		}

		data.Variants = append(data.Variants, variantData{
			ID:      v.ID,
			SKU:     v.SKU,
			Price:   v.Price,
			Options: opts,
			Media:   media,
		})
	}

	return data
}

// strOrEmpty returns the empty string when s is nil, otherwise *s.
func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toProductDetailData(p domain.ProductDetail) productDetailData {
	data := productDetailData{
		ID:          p.ID,
		Handle:      p.Handle,
		Title:       p.Title,
		Status:      p.Status,
		Vendor:      p.Vendor,
		Description: p.Description,
		Category:    productCategoryData{Id: p.Category.Id, Slug: p.Category.Slug, Name: p.Category.Name},
		Options:     make([]productOptionData, 0, len(p.Options)),
		Variants:    make([]variantDetailData, 0, len(p.Variants)),
		CreatedAt:   p.CreatedAt,
		Galleries:   toGalleryData(p.Media),
	}

	for _, opt := range p.Options {
		values := make([]productOptionValueData, 0, len(opt.Values))
		for _, value := range opt.Values {
			values = append(values, productOptionValueData{
				ID:       value.ID,
				Value:    value.Value,
				Position: value.Position,
			})
		}
		data.Options = append(data.Options, productOptionData{
			ID:       opt.ID,
			Name:     opt.Name,
			Position: opt.Position,
			Values:   values,
		})
	}

	for _, v := range p.Variants {
		var opts []domain.VariantOption
		_ = json.Unmarshal(v.Options, &opts)
		if opts == nil {
			opts = []domain.VariantOption{}
		}

		media := make([]variantMediaData, 0, len(v.Media))
		for _, m := range v.Media {
			media = append(media, variantMediaData{
				ID:       m.MediaID,
				Position: m.Position,
			})
		}

		data.Variants = append(data.Variants, variantDetailData{
			ID:      v.ID,
			Title:   strOrEmpty(v.Title),
			Barcode: strOrEmpty(v.Barcode),
			SKU:     v.SKU,
			Price:   v.Price,
			Stock:   v.Stock,
			Options: opts,
			Media:   media,
			Weight:  v.Weight,
		})
	}

	return data
}

// variantResponseData is the response shape for standalone variant CRUD
// endpoints (create, update, restore, bulk).
type variantResponseData struct {
	ID        uuid.UUID       `json:"id"`
	ProductID uuid.UUID       `json:"product_id"`
	SKU       *string         `json:"sku,omitempty"`
	Barcode   *string         `json:"barcode,omitempty"`
	Title     *string         `json:"title,omitempty"`
	Price     decimal.Decimal `json:"price"`
	Weight    decimal.Decimal `json:"weight"`
	Position  int             `json:"position"`
	IsDeleted bool            `json:"is_deleted"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}

// productGalleryItemData is the shape of each entry in productDetailData.Galleries.
type productGalleryItemData struct {
	ID       uuid.UUID `json:"id"`
	Type     string    `json:"type"`
	URL      string    `json:"url"`
	AltText  *string   `json:"altText,omitempty"`
	Position int       `json:"position"`
}

func toGalleryData(media []domain.ProductMedia) []productGalleryItemData {
	data := make([]productGalleryItemData, 0, len(media))
	for _, m := range media {
		data = append(data, productGalleryItemData{
			ID:       m.ID,
			Type:     m.Type,
			URL:      m.URL,
			AltText:  m.AltText,
			Position: m.Position,
		})
	}
	return data
}

// mediaResponseData is the response shape for product gallery media endpoints.
type mediaResponseData struct {
	ID        uuid.UUID  `json:"id"`
	ProductID uuid.UUID  `json:"product_id"`
	Type      string     `json:"type"`
	URL       string     `json:"url"`
	AltText   *string    `json:"alt_text,omitempty"`
	Position  int        `json:"position"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// variantMediaResponseData is the response shape for variant_media links.
type variantMediaResponseData struct {
	VariantID uuid.UUID `json:"variant_id"`
	MediaID   uuid.UUID `json:"media_id"`
	Position  int       `json:"position"`
}

func toVariantResponseData(v domain.Variant) variantResponseData {
	return variantResponseData{
		ID:        v.ID,
		ProductID: v.ProductID,
		SKU:       v.SKU,
		Barcode:   v.Barcode,
		Title:     v.Title,
		Price:     v.Price,
		Weight:    v.Weight,
		Position:  v.Position,
		IsDeleted: v.IsDeleted,
		CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	}
}

func toVariantResponseSlice(variants []domain.Variant) []variantResponseData {
	data := make([]variantResponseData, 0, len(variants))
	for _, v := range variants {
		data = append(data, toVariantResponseData(v))
	}
	return data
}

func toMediaResponseData(m domain.ProductMedia) mediaResponseData {
	return mediaResponseData{
		ID:        m.ID,
		ProductID: m.ProductID,
		Type:      m.Type,
		URL:       m.URL,
		AltText:   m.AltText,
		Position:  m.Position,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func toMediaResponseSlice(media []domain.ProductMedia) []mediaResponseData {
	data := make([]mediaResponseData, 0, len(media))
	for _, m := range media {
		data = append(data, toMediaResponseData(m))
	}
	return data
}

func toVariantMediaResponseData(vm domain.VariantMedia) variantMediaResponseData {
	return variantMediaResponseData{
		VariantID: vm.VariantID,
		MediaID:   vm.MediaID,
		Position:  vm.Position,
	}
}
