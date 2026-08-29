package dto

import (
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// CreateReviewRequest is the request body for POST /products/{productId}/reviews.
type CreateReviewRequest struct {
	VariantID *uuid.UUID `json:"variant_id"`
	Rating    int        `json:"rating" binding:"required" validate:"required,min=1,max=5"`
	Title     *string    `json:"title" validate:"omitempty,max=150"`
	Comment   *string    `json:"comment" validate:"omitempty,max=5000"`
}

func (r CreateReviewRequest) ToDomain() domain.CreateReviewInput {
	return domain.CreateReviewInput{
		VariantID: r.VariantID,
		Rating:    r.Rating,
		Title:     r.Title,
		Comment:   r.Comment,
	}
}

// UpdateReviewRequest is the request body for PATCH /reviews/{reviewId}.
// Nil fields mean "leave unchanged"; at least one field must be present.
type UpdateReviewRequest struct {
	Rating  *int    `json:"rating" validate:"omitempty,min=1,max=5"`
	Title   *string `json:"title" validate:"omitempty,max=150"`
	Comment *string `json:"comment" validate:"omitempty,max=5000"`
}

func (r UpdateReviewRequest) ToDomain() domain.UpdateReviewInput {
	return domain.UpdateReviewInput{
		Rating:  r.Rating,
		Title:   r.Title,
		Comment: r.Comment,
	}
}

// ReviewUserResponse is the minimal hydrated user shape exposed on public
// review responses (instead of the raw user_id).
type ReviewUserResponse struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
}

// ReviewResponse is the canonical review object (section 1 / common reference).
type ReviewResponse struct {
	ID        uuid.UUID  `json:"id"`
	ProductID uuid.UUID  `json:"product_id"`
	VariantID *uuid.UUID `json:"variant_id"`
	UserID    uuid.UUID  `json:"user_id"`
	Rating    int        `json:"rating"`
	Title     *string    `json:"title"`
	Comment   *string    `json:"comment"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
}

// ReviewListItemResponse is the section 2 / section 4 list shape, replacing
// user_id with the hydrated user object.
type ReviewListItemResponse struct {
	ID        uuid.UUID         `json:"id"`
	ProductID uuid.UUID         `json:"product_id"`
	VariantID *uuid.UUID        `json:"variant_id"`
	User      ReviewUserResponse `json:"user"`
	Rating    int               `json:"rating"`
	Title     *string           `json:"title"`
	Comment   *string           `json:"comment"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt *time.Time        `json:"updated_at"`
}

// RatingSummaryResponse is the section 3 aggregate shape.
type RatingSummaryResponse struct {
	ProductID       uuid.UUID       `json:"product_id"`
	AverageRating   decimal.Decimal `json:"average_rating"`
	TotalReviews    int             `json:"total_reviews"`
	RatingBreakdown map[int]int     `json:"rating_breakdown"`
}

// UserReviewProductResponse is the hydrated product shape inside the "My
// Reviews" listing.
type UserReviewProductResponse struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ThumbnailURL *string   `json:"thumbnail_url"`
}

// UserReviewListItemResponse is the section 7 list shape.
type UserReviewListItemResponse struct {
	ID        uuid.UUID                `json:"id"`
	Product   UserReviewProductResponse `json:"product"`
	VariantID *uuid.UUID               `json:"variant_id"`
	Rating    int                      `json:"rating"`
	Title     *string                  `json:"title"`
	Comment   *string                  `json:"comment"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt *time.Time               `json:"updated_at"`
}

// PaginationResponse carries page metadata for list endpoints.
type PaginationResponse struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

// ReviewListResponse is the section 2 response envelope.
type ReviewListResponse struct {
	Data       []ReviewListItemResponse `json:"data"`
	Pagination PaginationResponse       `json:"pagination"`
}

// UserReviewListResponse is the section 7 response envelope.
type UserReviewListResponse struct {
	Data       []UserReviewListItemResponse `json:"data"`
	Pagination PaginationResponse           `json:"pagination"`
}

// ToReviewResponse maps a domain review to the canonical response shape.
func ToReviewResponse(r domain.Review) ReviewResponse {
	return ReviewResponse{
		ID:        r.ID,
		ProductID: r.ProductID,
		VariantID: r.VariantID,
		UserID:    r.UserID,
		Rating:    r.Rating,
		Title:     r.Title,
		Comment:   r.Comment,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// ToReviewListItemResponse maps a domain review to the public list shape.
func ToReviewListItemResponse(r domain.Review) ReviewListItemResponse {
	return ReviewListItemResponse{
		ID:        r.ID,
		ProductID: r.ProductID,
		VariantID: r.VariantID,
		User: ReviewUserResponse{
			ID:          r.UserID,
			DisplayName: r.DisplayName,
		},
		Rating:    r.Rating,
		Title:     r.Title,
		Comment:   r.Comment,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// ToRatingSummaryResponse maps a domain rating summary to its response shape.
func ToRatingSummaryResponse(s domain.RatingSummary) RatingSummaryResponse {
	return RatingSummaryResponse{
		ProductID:       s.ProductID,
		AverageRating:   s.AverageRating,
		TotalReviews:    s.TotalReviews,
		RatingBreakdown: s.RatingBreakdown,
	}
}

// ToUserReviewListItemResponse maps a hydrated review to the section 7 shape.
func ToUserReviewListItemResponse(r domain.ReviewWithProduct) UserReviewListItemResponse {
	return UserReviewListItemResponse{
		ID: r.ID,
		Product: UserReviewProductResponse{
			ID:           r.ProductID,
			Name:         r.ProductName,
			ThumbnailURL: r.ThumbnailURL,
		},
		VariantID: r.VariantID,
		Rating:    r.Rating,
		Title:     r.Title,
		Comment:   r.Comment,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}
