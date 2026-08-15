package domain

import (
	"context"
	"time"
)

// type Category struct {
// 	Pesan string `json:"pesan"`
// }

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

type QueryCategoryUseCase interface {
	FindAll(ctx context.Context) ([]Category, error)
}

type CategoryRepository interface {
	FindAll(ctx context.Context) ([]Category, error)
}
