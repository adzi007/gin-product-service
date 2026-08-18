package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ProductHandler struct {
	insertUseCase domain.InsertProductUseCase
}

func NewProductHandler(insertUseCase domain.InsertProductUseCase) *ProductHandler {
	return &ProductHandler{
		insertUseCase: insertUseCase,
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
