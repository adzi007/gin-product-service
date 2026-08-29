package handler

import (
	"net/http"
	"strconv"

	"gin-product-service/internal/delivery/http/dto"
	"gin-product-service/internal/delivery/http/middleware"
	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type ReviewHandler struct {
	insertUseCase  domain.InsertReviewUseCase
	queryUseCase   domain.QueryReviewUseCase
	updateUseCase  domain.UpdateReviewUseCase
	deleteUseCase  domain.DeleteReviewUseCase
	summaryUseCase domain.SummaryReviewUseCase
}

func NewReviewHandler(
	insertUseCase domain.InsertReviewUseCase,
	queryUseCase domain.QueryReviewUseCase,
	updateUseCase domain.UpdateReviewUseCase,
	deleteUseCase domain.DeleteReviewUseCase,
	summaryUseCase domain.SummaryReviewUseCase,
) *ReviewHandler {
	return &ReviewHandler{
		insertUseCase:  insertUseCase,
		queryUseCase:   queryUseCase,
		updateUseCase:  updateUseCase,
		deleteUseCase:  deleteUseCase,
		summaryUseCase: summaryUseCase,
	}
}

// Create godoc
// @Summary      Create a review for a product
// @Description  Create a review for a product, optionally tied to a purchased variant. Requires authentication.
// @Tags         reviews
// @Accept       json
// @Produce      json
// @Param        id   path string true "Product UUID"
// @Param        body body dto.CreateReviewRequest true "Review to create"
// @Success      201  {object} dto.ReviewResponse
// @Failure      400  {object} map[string]any
// @Failure      401  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      409  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/reviews [post]
func (h *ReviewHandler) Create(c *gin.Context) {

	ctx := c.Request.Context()

	productID, ok := parseReviewUUID(c, "id")
	if !ok {
		return
	}

	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, reviewErrorResponse("UNAUTHENTICATED", "missing user identity"))
		return
	}
	name, _ := middleware.GetUserName(c)

	var input dto.CreateReviewRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", reviewValidationMessage(err)))
		return
	}

	created, err := h.insertUseCase.Create(ctx, productID, userID, name, input.ToDomain())
	if err != nil {
		status, code, message := mapReviewError(err)
		c.JSON(status, reviewErrorResponse(code, message))
		return
	}

	c.JSON(http.StatusCreated, dto.ToReviewResponse(created))
}

// Fetch godoc
// @Summary      List reviews for a product
// @Description  Get a paginated, filterable, sortable list of reviews for a product
// @Tags         reviews
// @Produce      json
// @Param        id            path string false "Product UUID"
// @Param        page          query int    false "Page number (1-indexed)"
// @Param        limit         query int    false "Page size (max 100)"
// @Param        rating        query int    false "Filter to a specific star rating (1-5)"
// @Param        verified_only query bool   false "Accepted but not filtered (no purchase history data)"
// @Param        has_comment   query bool   false "Only reviews with a non-empty comment"
// @Param        sort          query string false "Sort: newest, oldest, highest_rating, lowest_rating"
// @Success      200  {object} dto.ReviewListResponse
// @Failure      400  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/reviews [get]
func (h *ReviewHandler) Fetch(c *gin.Context) {

	ctx := c.Request.Context()

	productID, ok := parseReviewUUID(c, "id")
	if !ok {
		return
	}

	page, _ := strconv.Atoi(c.Query("page"))
	limit, _ := strconv.Atoi(c.Query("limit"))

	var rating *int
	if raw := c.Query("rating"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 5 {
			c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", "rating must be an integer between 1 and 5"))
			return
		}
		rating = &v
	}

	var hasComment *bool
	if raw := c.Query("has_comment"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", "has_comment must be a boolean"))
			return
		}
		hasComment = &v
	}

	sortParam := c.DefaultQuery("sort", "newest")
	if sortParam != "newest" && sortParam != "oldest" && sortParam != "highest_rating" && sortParam != "lowest_rating" {
		c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", "sort must be one of newest, oldest, highest_rating, lowest_rating"))
		return
	}

	// verified_only is accepted but intentionally not filtered: purchase
	// history data does not exist in this service (see specs/reviews-api-spec.md
	// scope decision #1). We read it here only to document the no-op.
	_ = c.Query("verified_only")

	filter := domain.ReviewFilter{
		Page:       page,
		Limit:      limit,
		Rating:     rating,
		HasComment: hasComment,
		Sort:       sortParam,
	}

	data, err := h.queryUseCase.ListByProduct(ctx, productID, filter)
	if err != nil {
		status, code, message := mapReviewError(err)
		c.JSON(status, reviewErrorResponse(code, message))
		return
	}

	items := make([]dto.ReviewListItemResponse, 0, len(data.Data))
	for _, r := range data.Data {
		items = append(items, dto.ToReviewListItemResponse(r))
	}

	c.JSON(http.StatusOK, dto.ReviewListResponse{
		Data: items,
		Pagination: dto.PaginationResponse{
			Page:       data.Page,
			Limit:      data.Limit,
			TotalItems: data.TotalItems,
			TotalPages: data.TotalPages,
		},
	})
}

// Summary godoc
// @Summary      Get a product's rating summary
// @Description  Get the aggregate rating stats (average, total, breakdown) for a product
// @Tags         reviews
// @Produce      json
// @Param        id path string true "Product UUID"
// @Success      200  {object} dto.RatingSummaryResponse
// @Failure      500  {object} map[string]any
// @Router       /products/{id}/reviews/summary [get]
func (h *ReviewHandler) Summary(c *gin.Context) {

	ctx := c.Request.Context()

	productID, ok := parseReviewUUID(c, "id")
	if !ok {
		return
	}

	summary, err := h.summaryUseCase.GetSummary(ctx, productID)
	if err != nil {
		status, code, message := mapReviewError(err)
		c.JSON(status, reviewErrorResponse(code, message))
		return
	}

	c.JSON(http.StatusOK, dto.ToRatingSummaryResponse(summary))
}

// GetByID godoc
// @Summary      Get a single review
// @Description  Get a single review by id (same shape as the list item)
// @Tags         reviews
// @Produce      json
// @Param        reviewId path string true "Review UUID"
// @Success      200  {object} dto.ReviewListItemResponse
// @Failure      400  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /reviews/{reviewId} [get]
func (h *ReviewHandler) GetByID(c *gin.Context) {

	ctx := c.Request.Context()

	reviewID, ok := parseReviewUUID(c, "reviewId")
	if !ok {
		return
	}

	review, err := h.queryUseCase.GetByID(ctx, reviewID)
	if err != nil {
		status, code, message := mapReviewError(err)
		c.JSON(status, reviewErrorResponse(code, message))
		return
	}

	c.JSON(http.StatusOK, dto.ToReviewListItemResponse(review))
}

// Update godoc
// @Summary      Update your own review
// @Description  Update a review's rating/title/comment. Only the author can edit it. Requires authentication.
// @Tags         reviews
// @Accept       json
// @Produce      json
// @Param        reviewId path string true "Review UUID"
// @Param        body     body dto.UpdateReviewRequest true "Fields to update"
// @Success      200  {object} dto.ReviewResponse
// @Failure      400  {object} map[string]any
// @Failure      401  {object} map[string]any
// @Failure      403  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /reviews/{reviewId} [patch]
func (h *ReviewHandler) Update(c *gin.Context) {

	ctx := c.Request.Context()

	reviewID, ok := parseReviewUUID(c, "reviewId")
	if !ok {
		return
	}

	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, reviewErrorResponse("UNAUTHENTICATED", "missing user identity"))
		return
	}
	name, _ := middleware.GetUserName(c)

	var input dto.UpdateReviewRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", err.Error()))
		return
	}

	if err := validate.Struct(input); err != nil {
		c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", reviewValidationMessage(err)))
		return
	}

	if input.Rating == nil && input.Title == nil && input.Comment == nil {
		c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", "at least one of rating, title or comment is required"))
		return
	}

	updated, err := h.updateUseCase.Update(ctx, reviewID, userID, name, input.ToDomain())
	if err != nil {
		status, code, message := mapReviewError(err)
		c.JSON(status, reviewErrorResponse(code, message))
		return
	}

	c.JSON(http.StatusOK, dto.ToReviewResponse(updated))
}

// Delete godoc
// @Summary      Delete your own review
// @Description  Delete a review. Only the author can delete it. Requires authentication.
// @Tags         reviews
// @Produce      json
// @Param        reviewId path string true "Review UUID"
// @Success      204  "No Content"
// @Failure      401  {object} map[string]any
// @Failure      403  {object} map[string]any
// @Failure      404  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /reviews/{reviewId} [delete]
func (h *ReviewHandler) Delete(c *gin.Context) {

	ctx := c.Request.Context()

	reviewID, ok := parseReviewUUID(c, "reviewId")
	if !ok {
		return
	}

	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, reviewErrorResponse("UNAUTHENTICATED", "missing user identity"))
		return
	}

	if err := h.deleteUseCase.Delete(ctx, reviewID, userID); err != nil {
		status, code, message := mapReviewError(err)
		c.JSON(status, reviewErrorResponse(code, message))
		return
	}

	c.Status(http.StatusNoContent)
}

// FetchMine godoc
// @Summary      List the current user's reviews
// @Description  Get a paginated list of the authenticated user's reviews. Requires authentication.
// @Tags         reviews
// @Produce      json
// @Param        page  query int false "Page number (1-indexed)"
// @Param        limit query int false "Page size (max 100)"
// @Success      200  {object} dto.UserReviewListResponse
// @Failure      401  {object} map[string]any
// @Failure      500  {object} map[string]any
// @Router       /users/me/reviews [get]
func (h *ReviewHandler) FetchMine(c *gin.Context) {

	ctx := c.Request.Context()

	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, reviewErrorResponse("UNAUTHENTICATED", "missing user identity"))
		return
	}

	page, _ := strconv.Atoi(c.Query("page"))
	limit, _ := strconv.Atoi(c.Query("limit"))

	data, err := h.queryUseCase.ListByUser(ctx, userID, page, limit)
	if err != nil {
		status, code, message := mapReviewError(err)
		c.JSON(status, reviewErrorResponse(code, message))
		return
	}

	items := make([]dto.UserReviewListItemResponse, 0, len(data.Data))
	for _, r := range data.Data {
		items = append(items, dto.ToUserReviewListItemResponse(r))
	}

	c.JSON(http.StatusOK, dto.UserReviewListResponse{
		Data: items,
		Pagination: dto.PaginationResponse{
			Page:       data.Page,
			Limit:      data.Limit,
			TotalItems: data.TotalItems,
			TotalPages: data.TotalPages,
		},
	})
}

// parseReviewUUID parses a path parameter as a UUID and writes the spec error
// shape on failure.
func parseReviewUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, reviewErrorResponse("ERR_VALIDATION", "invalid "+param))
		return uuid.Nil, false
	}
	return id, true
}

// reviewErrorResponse writes the standard review spec error shape.
func reviewErrorResponse(code, message string) gin.H {
	return gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
			"details": gin.H{},
		},
	}
}

// reviewValidationMessage flattens validator errors into a single message.
func reviewValidationMessage(err error) string {
	if fieldErrs, ok := err.(validator.ValidationErrors); ok && len(fieldErrs) > 0 {
		return fieldValidationMessage(fieldErrs[0])
	}
	return err.Error()
}

// mapReviewError maps domain errors to the spec's HTTP statuses and error codes.
func mapReviewError(err error) (int, string, string) {
	switch err {
	case domain.ErrReviewInvalidInput:
		return http.StatusBadRequest, "ERR_VALIDATION", err.Error()
	case domain.ErrReviewProductNotFound:
		return http.StatusNotFound, "PRODUCT_NOT_FOUND", "Product not found"
	case domain.ErrReviewVariantNotFound:
		return http.StatusNotFound, "VARIANT_NOT_FOUND", "Variant not found"
	case domain.ErrReviewNotFound:
		return http.StatusNotFound, "REVIEW_NOT_FOUND", "Review not found"
	case domain.ErrReviewAlreadyExists:
		return http.StatusConflict, "REVIEW_ALREADY_EXISTS", "You have already reviewed this product."
	case domain.ErrReviewForbidden:
		return http.StatusForbidden, "FORBIDDEN", "You are not allowed to perform this action"
	default:
		return http.StatusInternalServerError, "ERR_INTERNAL", err.Error()
	}
}
