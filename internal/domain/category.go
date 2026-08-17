package domain

import (
	"context"
	"errors"
	"time"
)

type Category struct {
	ID          int        `json:"id" db:"id"`
	Name        string     `json:"name" db:"name"`
	Slug        string     `json:"slug" db:"slug"`
	Thumbnail   *string    `json:"thumbnail" db:"thumbnail"`
	Description *string    `json:"description" db:"description"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at" db:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at" db:"deleted_at"`
}

// ErrCategoryNotFound is returned when a category does not exist or
// has been soft-deleted.
var ErrCategoryNotFound = errors.New("category not found")

// ErrCategoryInvalidInput is returned when a create/update request fails
// basic validation (e.g. an empty name) at the use case layer.
var ErrCategoryInvalidInput = errors.New("invalid category input")

// ListCategoryParams holds filtering, pagination and sorting options
// for the list categories endpoint.
type ListCategoryParams struct {
	Name    string
	Page    int
	PerPage int
	SortBy  string // "name" | "created_at"
	SortDir string // "asc" | "desc"
}

// PaginatedCategories carries the page data plus pagination metadata.
type PaginatedCategories struct {
	Data       []Category `json:"data"`
	Total      int        `json:"total"`
	Page       int        `json:"page"`
	PerPage    int        `json:"per_page"`
	TotalPages int        `json:"total_pages"`
}

// CategoryOption is a lightweight shape used for dropdowns.
type CategoryOption struct {
	ID   int    `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
}

// CreateCategoryInput is the request body for creating a category.
type CreateCategoryInput struct {
	Name        string  `json:"name" binding:"required"`
	Thumbnail   *string `json:"thumbnail"`
	Description *string `json:"description"`
}

// UpdateCategoryInput is the request body for updating a category.
type UpdateCategoryInput struct {
	Name        string  `json:"name" binding:"required"`
	Thumbnail   *string `json:"thumbnail"`
	Description *string `json:"description"`
}

type QueryCategoryUseCase interface {
	FindAll(ctx context.Context, params ListCategoryParams) (PaginatedCategories, error)
	FindAllForDropdown(ctx context.Context, name string) ([]CategoryOption, error)
	GetByID(ctx context.Context, id int) (Category, error)
}

type InsertCategoryUseCase interface {
	Create(ctx context.Context, input CreateCategoryInput) (Category, error)
}

type UpdateCategoryUseCase interface {
	Update(ctx context.Context, id int, input UpdateCategoryInput) (Category, error)
}

type DeleteCategoryUseCase interface {
	Delete(ctx context.Context, id int) error
}

type CategoryRepository interface {
	FindAll(ctx context.Context, params ListCategoryParams) ([]Category, int, error)
	FindAllForDropdown(ctx context.Context, name string) ([]CategoryOption, error)
	Create(ctx context.Context, category Category) (Category, error)
	FindByID(ctx context.Context, id int) (Category, error)
	Update(ctx context.Context, id int, category Category) (Category, error)
	Delete(ctx context.Context, id int) error
}
