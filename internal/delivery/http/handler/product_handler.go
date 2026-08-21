package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ProductHandler struct {
	insertUseCase domain.InsertProductUseCase
	queryUseCase  domain.QueryProductUseCase
}

func NewProductHandler(insertUseCase domain.InsertProductUseCase, queryUseCase domain.QueryProductUseCase) *ProductHandler {
	return &ProductHandler{
		insertUseCase: insertUseCase,
		queryUseCase:  queryUseCase,
	}
}

// Create godoc
// @Summary      Create a product
// @Description  Create a product with options, variants, gallery and initial stock in one transaction
// @Tags         products
// @Accept       json
// @Produce      json
// @Param        body body domain.CreateProductInput true "Product to create"
// @Success      201  {object} map[string]any
// @Failure      400  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products [post]
func (h *ProductHandler) Create(c *gin.Context) {

	ctx := c.Request.Context()

	var input domain.CreateProductInput
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

	created, err := h.insertUseCase.Create(ctx, input)
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
// @Param        search   query string false "Filter by partial (case-insensitive) title or handle match"
// @Param        page     query int    false "Page number (1-indexed)"
// @Param        per_page query int    false "Items per page"
// @Param        sort_by  query string false "Sort column. One of: title, created_at"
// @Param        sort_dir query string false "Sort direction. One of: asc, desc"
// @Success      200      {object} domain.PaginatedProducts
// @Failure      400      {object} map[string]any
// @Failure      500      {object} map[string]any
// @Router       /products [get]
func (h *ProductHandler) Fetch(c *gin.Context) {

	ctx := c.Request.Context()

	page, _ := strconv.Atoi(c.Query("page"))
	perPage, _ := strconv.Atoi(c.Query("per_page"))

	sortBy := strings.ToLower(c.Query("sort_by"))
	if sortBy == "" {
		sortBy = "created_at"
	}
	if sortBy != "title" && sortBy != "created_at" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort_by: must be one of title, created_at"})
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

	params := domain.ListProductParams{
		Search:  c.Query("search"),
		Page:    page,
		PerPage: perPage,
		SortBy:  sortBy,
		SortDir: sortDir,
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
	default:
		return http.StatusInternalServerError, "ERR_INTERNAL"
	}
}

type productData struct {
	ID         uuid.UUID           `json:"id"`
	Handle     string              `json:"handle"`
	Title      string              `json:"title"`
	Vendor     *string             `json:"vendor,omitempty"`
	CategoryID int                 `json:"category_id"`
	Options    []productOptionData `json:"options"`
	Variants   []variantData       `json:"variants"`
	CreatedAt  time.Time           `json:"created_at"`
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
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// productDetailData is the response shape for GET /products/:id_or_handle.
// Unlike productData (used by Create), it exposes a nested category object
// instead of category_id and adds per-variant stock.
type productDetailData struct {
	ID        uuid.UUID           `json:"id"`
	Handle    string              `json:"handle"`
	Title     string              `json:"title"`
	Vendor    *string             `json:"vendor,omitempty"`
	Category  productCategoryData `json:"category"`
	Options   []productOptionData `json:"options"`
	Variants  []variantDetailData `json:"variants"`
	CreatedAt time.Time           `json:"created_at"`
}

type variantDetailData struct {
	ID    uuid.UUID       `json:"id"`
	SKU   *string         `json:"sku,omitempty"`
	Price decimal.Decimal `json:"price"`
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

func toProductDetailData(p domain.Product) productDetailData {
	data := productDetailData{
		ID:        p.ID,
		Handle:    p.Handle,
		Title:     p.Title,
		Vendor:    p.Vendor,
		Category:  productCategoryData{Slug: p.Category.Slug, Name: p.Category.Name},
		Options:   make([]productOptionData, 0, len(p.Options)),
		Variants:  make([]variantDetailData, 0, len(p.Variants)),
		CreatedAt: p.CreatedAt,
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
			SKU:     v.SKU,
			Price:   v.Price,
			Stock:   v.Stock,
			Options: opts,
			Media:   media,
		})
	}

	return data
}
