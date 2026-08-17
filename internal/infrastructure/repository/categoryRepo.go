package repository

import (
	"context"
	"fmt"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/metrics"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a name into a URL-friendly slug:
// lowercase, non-alphanumeric chars become single hyphens, trailing hyphens trimmed.
func slugify(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = nonAlphanumeric.ReplaceAllString(slug, "-")
	return strings.Trim(slug, "-")
}

type categoryRepo struct {
	db database.Database
}

func NewCategoryRepo(db database.Database) domain.CategoryRepository {
	return &categoryRepo{db: db}
}

func (r *categoryRepo) FindAll(ctx context.Context, params domain.ListCategoryParams) ([]domain.Category, int, error) {

	defer metrics.ObserveDB("category", "find_all")(time.Now())

	// Build the base conditions. sort_by / sort_dir are whitelisted again here
	// (defense in depth) so they never come from raw user input.
	where := "WHERE deleted_at IS NULL"

	args := []interface{}{}
	argIdx := 0

	if params.Name != "" {
		argIdx++
		args = append(args, "%"+params.Name+"%")
		where += fmt.Sprintf(" AND name ILIKE $%d", argIdx)
	}

	// total count matching filters (before pagination)
	countQuery := "SELECT COUNT(*) FROM category " + where
	var total int
	if err := r.db.GetDb().QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// whitelist ORDER BY (only allow-list values, never raw user input)
	sortBy := "name"
	if params.SortBy == "created_at" {
		sortBy = "created_at"
	}

	sortDir := "ASC"
	if params.SortDir == "desc" {
		sortDir = "DESC"
	}

	offset := (params.Page - 1) * params.PerPage

	query := fmt.Sprintf(`
		SELECT id, name, slug, thumbnail, description, created_at, updated_at, deleted_at 
		FROM category 
		%s 
		ORDER BY %s %s 
		LIMIT $%d OFFSET $%d
	`, where, sortBy, sortDir, argIdx+1, argIdx+2)

	args = append(args, params.PerPage, offset)

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	categories, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.Category])
	if err != nil {
		return nil, 0, err
	}

	return categories, total, nil
}

func (r *categoryRepo) FindAllForDropdown(ctx context.Context, name string) ([]domain.CategoryOption, error) {

	defer metrics.ObserveDB("category", "find_all_dropdown")(time.Now())

	query := `
		SELECT id, name
		FROM category
		WHERE deleted_at IS NULL
	`

	args := []interface{}{}
	if name != "" {
		query += " AND name ILIKE $1"
		args = append(args, "%"+name+"%")
	}

	query += " ORDER BY name ASC LIMIT 500"

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	options, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.CategoryOption])
	if err != nil {
		return nil, err
	}

	return options, nil
}

func (r *categoryRepo) Create(ctx context.Context, category domain.Category) (domain.Category, error) {

	defer metrics.ObserveDB("category", "create")(time.Now())

	// Generate the slug from the name if it wasn't already set.
	if category.Slug == "" {
		category.Slug = slugify(category.Name)
	}

	query := `
		INSERT INTO category (name, slug, thumbnail, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, slug, thumbnail, description, created_at, updated_at, deleted_at
	`

	row := r.db.GetDb().QueryRow(ctx, query,
		category.Name,
		category.Slug,
		category.Thumbnail,
		category.Description,
	)

	var created domain.Category
	if err := row.Scan(
		&created.ID,
		&created.Name,
		&created.Slug,
		&created.Thumbnail,
		&created.Description,
		&created.CreatedAt,
		&created.UpdatedAt,
		&created.DeletedAt,
	); err != nil {
		return domain.Category{}, err
	}

	return created, nil
}

func (r *categoryRepo) FindByID(ctx context.Context, id int) (domain.Category, error) {

	defer metrics.ObserveDB("category", "find_by_id")(time.Now())

	query := `
		SELECT id, name, slug, thumbnail, description, created_at, updated_at, deleted_at
		FROM category
		WHERE id = $1 AND deleted_at IS NULL
	`

	row := r.db.GetDb().QueryRow(ctx, query, id)

	var category domain.Category
	err := row.Scan(
		&category.ID,
		&category.Name,
		&category.Slug,
		&category.Thumbnail,
		&category.Description,
		&category.CreatedAt,
		&category.UpdatedAt,
		&category.DeletedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Category{}, domain.ErrCategoryNotFound
		}
		return domain.Category{}, err
	}

	return category, nil
}

func (r *categoryRepo) Update(ctx context.Context, id int, category domain.Category) (domain.Category, error) {

	defer metrics.ObserveDB("category", "update")(time.Now())

	// Regenerate the slug from the new name.
	if category.Slug == "" {
		category.Slug = slugify(category.Name)
	}

	query := `
		UPDATE category
		SET name = $1, slug = $2, thumbnail = $3, description = $4, updated_at = now()
		WHERE id = $5 AND deleted_at IS NULL
		RETURNING id, name, slug, thumbnail, description, created_at, updated_at, deleted_at
	`

	row := r.db.GetDb().QueryRow(ctx, query,
		category.Name,
		category.Slug,
		category.Thumbnail,
		category.Description,
		id,
	)

	var updated domain.Category
	err := row.Scan(
		&updated.ID,
		&updated.Name,
		&updated.Slug,
		&updated.Thumbnail,
		&updated.Description,
		&updated.CreatedAt,
		&updated.UpdatedAt,
		&updated.DeletedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Category{}, domain.ErrCategoryNotFound
		}
		return domain.Category{}, err
	}

	return updated, nil
}

func (r *categoryRepo) Delete(ctx context.Context, id int) error {

	defer metrics.ObserveDB("category", "delete")(time.Now())

	query := `UPDATE category SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`

	tag, err := r.db.GetDb().Exec(ctx, query, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return domain.ErrCategoryNotFound
	}

	return nil
}
