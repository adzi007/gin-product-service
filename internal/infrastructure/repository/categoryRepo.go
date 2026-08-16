package repository

import (
	"context"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/metrics"
	"time"

	"github.com/jackc/pgx/v5"
)

type categoryRepo struct {
	db database.Database
}

func NewCategoryRepo(db database.Database) domain.CategoryRepository {
	return &categoryRepo{db: db}
}

func (r *categoryRepo) FindAll(ctx context.Context) ([]domain.Category, error) {

	defer metrics.ObserveDB("category", "find_all")(time.Now())

	query := `
		SELECT id, name, slug, thumbnail, description, created_at, updated_at, deleted_at 
		FROM category 
		ORDER BY name ASC 
		LIMIT 100
	`

	rows, err := r.db.GetDb().Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Automatically maps columns to struct db tags
	categories, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.Category])
	if err != nil {
		return nil, err
	}

	return categories, nil
}
