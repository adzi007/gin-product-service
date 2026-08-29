package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/metrics"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type reviewRepo struct {
	db database.Database
}

func NewReviewRepo(db database.Database) domain.ReviewRepository {
	return &reviewRepo{db: db}
}

var _ domain.ReviewRepository = (*reviewRepo)(nil)

// reviewColumns must stay in sync with domain.Review's db tags.
const reviewColumns = "id, product_id, variant_id, user_id, display_name, rating, title, comment, created_at, updated_at"

// translateReviewInsertError maps the uq_user_product_review unique violation
// (one review per user per product) to the domain conflict error.
func translateReviewInsertError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return domain.ErrReviewAlreadyExists
	}
	return err
}

// reviewSortClause whitelists the ORDER BY clause so raw user input is never
// interpolated into the query.
func reviewSortClause(sort string) string {
	switch sort {
	case "oldest":
		return "created_at ASC"
	case "highest_rating":
		return "rating DESC, created_at DESC"
	case "lowest_rating":
		return "rating ASC, created_at DESC"
	default:
		return "created_at DESC"
	}
}

func (r *reviewRepo) Insert(ctx context.Context, review *domain.Review) error {
	defer metrics.ObserveDB("review", "insert")(time.Now())

	row := r.db.GetDb().QueryRow(ctx, `
		INSERT INTO product_reviews (id, product_id, variant_id, user_id, display_name, rating, title, comment)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at`,
		pgUUID(review.ID),
		pgUUID(review.ProductID),
		pgUUIDPtr(review.VariantID),
		pgUUID(review.UserID),
		review.DisplayName,
		review.Rating,
		review.Title,
		review.Comment,
	)

	if err := row.Scan(&review.CreatedAt, &review.UpdatedAt); err != nil {
		return translateReviewInsertError(err)
	}
	return nil
}

func (r *reviewRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Review, error) {
	defer metrics.ObserveDB("review", "find_by_id")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `SELECT `+reviewColumns+` FROM product_reviews WHERE id = $1`, pgUUID(id))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	review, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.Review])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrReviewNotFound
		}
		return nil, err
	}
	return &review, nil
}

func (r *reviewRepo) FindByProduct(ctx context.Context, productID uuid.UUID, filter domain.ReviewFilter) ([]domain.Review, int, error) {
	defer metrics.ObserveDB("review", "find_by_product")(time.Now())

	where := "WHERE product_id = $1"
	args := []interface{}{pgUUID(productID)}
	argIdx := 1

	if filter.Rating != nil {
		argIdx++
		args = append(args, *filter.Rating)
		where += fmt.Sprintf(" AND rating = $%d", argIdx)
	}
	if filter.HasComment != nil {
		if *filter.HasComment {
			where += " AND comment IS NOT NULL AND comment <> ''"
		} else {
			where += " AND (comment IS NULL OR comment = '')"
		}
	}

	countQuery := "SELECT COUNT(*) FROM product_reviews " + where
	var total int
	if err := r.db.GetDb().QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (filter.Page - 1) * filter.Limit
	argIdx++
	limitIdx := argIdx
	args = append(args, filter.Limit)
	argIdx++
	offsetIdx := argIdx
	args = append(args, offset)

	query := fmt.Sprintf(
		"SELECT %s FROM product_reviews %s ORDER BY %s LIMIT $%d OFFSET $%d",
		reviewColumns, where, reviewSortClause(filter.Sort), limitIdx, offsetIdx,
	)

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	reviews, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.Review])
	if err != nil {
		return nil, 0, err
	}
	return reviews, total, nil
}

func (r *reviewRepo) FindByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]domain.ReviewWithProduct, int, error) {
	defer metrics.ObserveDB("review", "find_by_user")(time.Now())

	var total int
	if err := r.db.GetDb().QueryRow(ctx,
		`SELECT COUNT(*) FROM product_reviews WHERE user_id = $1`, pgUUID(userID)).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	query := `
		SELECT pr.id, pr.product_id, pr.variant_id, pr.user_id, pr.display_name,
		       pr.rating, pr.title, pr.comment, pr.created_at, pr.updated_at,
		       p.title AS product_name,
		       (SELECT pm.url
		          FROM product_media pm
		         WHERE pm.product_id = p.id AND pm.position = 1
		         LIMIT 1) AS thumbnail_url
		FROM product_reviews pr
		JOIN products p ON p.id = pr.product_id
		WHERE pr.user_id = $1
		ORDER BY pr.created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.GetDb().Query(ctx, query, pgUUID(userID), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	reviews, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.ReviewWithProduct])
	if err != nil {
		return nil, 0, err
	}
	return reviews, total, nil
}

func (r *reviewRepo) Update(ctx context.Context, review *domain.Review) error {
	defer metrics.ObserveDB("review", "update")(time.Now())

	row := r.db.GetDb().QueryRow(ctx, `
		UPDATE product_reviews
		SET rating = $2, title = $3, comment = $4, display_name = $5, updated_at = now()
		WHERE id = $1
		RETURNING created_at, updated_at`,
		pgUUID(review.ID),
		review.Rating,
		review.Title,
		review.Comment,
		review.DisplayName,
	)

	if err := row.Scan(&review.CreatedAt, &review.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrReviewNotFound
		}
		return err
	}
	return nil
}

func (r *reviewRepo) Delete(ctx context.Context, id uuid.UUID) error {
	defer metrics.ObserveDB("review", "delete")(time.Now())

	tag, err := r.db.GetDb().Exec(ctx, `DELETE FROM product_reviews WHERE id = $1`, pgUUID(id))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrReviewNotFound
	}
	return nil
}

func (r *reviewRepo) GetSummary(ctx context.Context, productID uuid.UUID) (*domain.RatingSummary, error) {
	defer metrics.ObserveDB("review", "get_summary")(time.Now())

	summary := &domain.RatingSummary{ProductID: productID}

	err := r.db.GetDb().QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(AVG(rating), 0)
		FROM product_reviews
		WHERE product_id = $1`,
		pgUUID(productID),
	).Scan(&summary.TotalReviews, &summary.AverageRating)
	if err != nil {
		return nil, err
	}

	rows, err := r.db.GetDb().Query(ctx, `
		SELECT rating, COUNT(*)
		FROM product_reviews
		WHERE product_id = $1
		GROUP BY rating`,
		pgUUID(productID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	breakdown := map[int]int{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}
	for rows.Next() {
		var rating, count int
		if err := rows.Scan(&rating, &count); err != nil {
			return nil, err
		}
		breakdown[rating] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	summary.RatingBreakdown = breakdown

	return summary, nil
}

func (r *reviewRepo) VariantBelongsToProduct(ctx context.Context, variantID, productID uuid.UUID) (bool, error) {
	defer metrics.ObserveDB("review", "variant_belongs_to_product")(time.Now())

	var belongs bool
	err := r.db.GetDb().QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM variants WHERE id = $1 AND product_id = $2
		)`,
		pgUUID(variantID), pgUUID(productID),
	).Scan(&belongs)
	if err != nil {
		return false, err
	}
	return belongs, nil
}

func (r *reviewRepo) ProductExists(ctx context.Context, productID uuid.UUID) (bool, error) {
	defer metrics.ObserveDB("review", "product_exists")(time.Now())

	var exists bool
	err := r.db.GetDb().QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM products WHERE id = $1)`,
		pgUUID(productID),
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
