package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Review is a single product review row. display_name is denormalized from the
// JWT name claim at write time (there is no local users table / user service to
// hydrate from), so listing endpoints can render it without a live lookup.
type Review struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	ProductID   uuid.UUID  `json:"product_id" db:"product_id"`
	VariantID   *uuid.UUID `json:"variant_id" db:"variant_id"`
	UserID      uuid.UUID  `json:"user_id" db:"user_id"`
	DisplayName string     `json:"display_name" db:"display_name"`
	Rating      int        `json:"rating" db:"rating"`
	Title       *string    `json:"title" db:"title"`
	Comment     *string    `json:"comment" db:"comment"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at" db:"updated_at"`
}

// ReviewFilter carries list-query parameters for GET /products/:id/reviews.
// VerifiedOnly is intentionally omitted: the verified_only query param is
// accepted by the handler but always treated as a no-op because purchase
// history data does not exist in this service (see specs/reviews-api-spec.md).
type ReviewFilter struct {
	Page       int
	Limit      int
	Rating     *int
	HasComment *bool
	Sort       string // "newest" (default), "oldest", "highest_rating", "lowest_rating"
}

// RatingSummary is the aggregate view exposed by GET /products/:id/reviews/summary.
type RatingSummary struct {
	ProductID       uuid.UUID       `json:"product_id" db:"product_id"`
	AverageRating   decimal.Decimal `json:"average_rating" db:"average_rating"`
	TotalReviews    int             `json:"total_reviews" db:"total_reviews"`
	RatingBreakdown map[int]int     `json:"rating_breakdown"`
}

// ReviewWithProduct hydrates a review with the product name and primary gallery
// thumbnail for the "My Reviews" listing (GET /users/me/reviews). It is flat
// (not embedding Review) so pgx.RowToStructByName can map every column by name.
type ReviewWithProduct struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	ProductID    uuid.UUID  `json:"product_id" db:"product_id"`
	VariantID    *uuid.UUID `json:"variant_id" db:"variant_id"`
	UserID       uuid.UUID  `json:"user_id" db:"user_id"`
	DisplayName  string     `json:"display_name" db:"display_name"`
	Rating       int        `json:"rating" db:"rating"`
	Title        *string    `json:"title" db:"title"`
	Comment      *string    `json:"comment" db:"comment"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    *time.Time `json:"updated_at" db:"updated_at"`
	ProductName  string     `json:"product_name" db:"product_name"`
	ThumbnailURL *string    `json:"thumbnail_url" db:"thumbnail_url"`
}

// PaginatedReviews carries a page of product reviews plus pagination metadata.
type PaginatedReviews struct {
	Data       []Review `json:"data"`
	Page       int      `json:"page"`
	Limit      int      `json:"limit"`
	TotalItems int      `json:"total_items"`
	TotalPages int      `json:"total_pages"`
}

// PaginatedUserReviews carries a page of the current user's reviews.
type PaginatedUserReviews struct {
	Data       []ReviewWithProduct `json:"data"`
	Page       int                 `json:"page"`
	Limit      int                 `json:"limit"`
	TotalItems int                 `json:"total_items"`
	TotalPages int                 `json:"total_pages"`
}

// CreateReviewInput is the application-level input for creating a review. The
// product id, user id and display name are supplied by the caller (handler),
// never taken from the request body.
type CreateReviewInput struct {
	VariantID *uuid.UUID
	Rating    int
	Title     *string
	Comment   *string
}

// UpdateReviewInput is the application-level input for updating a review.
// Nil fields mean "leave unchanged".
type UpdateReviewInput struct {
	Rating  *int
	Title   *string
	Comment *string
}

// Review errors surfaced by the review module.
var (
	// ErrReviewNotFound is returned when no review matches the given id.
	ErrReviewNotFound = errors.New("review not found")
	// ErrReviewAlreadyExists is returned when a user already reviewed a product
	// (uq_user_product_review unique violation).
	ErrReviewAlreadyExists = errors.New("review already exists")
	// ErrReviewInvalidInput is returned when a create/update request fails basic
	// validation (rating out of range, string too long).
	ErrReviewInvalidInput = errors.New("invalid review input")
	// ErrReviewForbidden is returned when a caller tries to update/delete a
	// review they do not own.
	ErrReviewForbidden = errors.New("not allowed to modify this review")
	// ErrReviewProductNotFound is returned when the product being reviewed does
	// not exist.
	ErrReviewProductNotFound = errors.New("product not found")
	// ErrReviewVariantNotFound is returned when the variant_id does not belong
	// to the product being reviewed.
	ErrReviewVariantNotFound = errors.New("variant not found")
)

// QueryReviewUseCase exposes the read-side review operations.
type QueryReviewUseCase interface {
	ListByProduct(ctx context.Context, productID uuid.UUID, filter ReviewFilter) (PaginatedReviews, error)
	GetByID(ctx context.Context, id uuid.UUID) (Review, error)
	ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) (PaginatedUserReviews, error)
}

// InsertReviewUseCase exposes review creation.
type InsertReviewUseCase interface {
	Create(ctx context.Context, productID, userID uuid.UUID, displayName string, input CreateReviewInput) (Review, error)
}

// UpdateReviewUseCase exposes review update (author only).
type UpdateReviewUseCase interface {
	Update(ctx context.Context, id, userID uuid.UUID, displayName string, input UpdateReviewInput) (Review, error)
}

// DeleteReviewUseCase exposes review deletion (author only).
type DeleteReviewUseCase interface {
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

// SummaryReviewUseCase exposes the aggregate rating summary.
type SummaryReviewUseCase interface {
	GetSummary(ctx context.Context, productID uuid.UUID) (RatingSummary, error)
}

// ReviewRepository is the persistence contract for product reviews.
type ReviewRepository interface {
	Insert(ctx context.Context, review *Review) error
	FindByID(ctx context.Context, id uuid.UUID) (*Review, error)
	FindByProduct(ctx context.Context, productID uuid.UUID, filter ReviewFilter) ([]Review, int, error)
	FindByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]ReviewWithProduct, int, error)
	Update(ctx context.Context, review *Review) error
	Delete(ctx context.Context, id uuid.UUID) error
	GetSummary(ctx context.Context, productID uuid.UUID) (*RatingSummary, error)
	VariantBelongsToProduct(ctx context.Context, variantID, productID uuid.UUID) (bool, error)
	ProductExists(ctx context.Context, productID uuid.UUID) (bool, error)
}
