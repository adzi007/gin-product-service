package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/metrics"
	"gin-product-service/internal/infrastructure/repository/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

type productRepo struct {
	db database.Database
}

func NewProductRepo(db database.Database) *productRepo {
	return &productRepo{db: db}
}

var (
	_ domain.ProductRepository = (*productRepo)(nil)
	_ domain.OptionRepository  = (*productRepo)(nil)
	_ domain.VariantRepository = (*productRepo)(nil)
	_ domain.MediaRepository   = (*productRepo)(nil)
)

// Create persists a product and all of its related rows (options, option
// values, variants, inventory items, stock moves, inventory levels, media and
// variant-media links) inside a single database transaction.
func (r *productRepo) Create(ctx context.Context, params domain.CreateProductParams) (domain.Product, error) {

	defer metrics.ObserveDB("product", "create")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return domain.Product{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Resolve the default location once per request, not per variant.
	var locationUUID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM locations WHERE is_default = true LIMIT 1`).Scan(&locationUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, domain.ErrDefaultLocationNotFound
		}
		return domain.Product{}, err
	}
	locationID := uuid.UUID(locationUUID.Bytes)

	product := params.Product

	// 1. products header.
	// var createdAt, updatedAt time.Time
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO products (id, handle, title, description, vendor, category_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`,
		pgUUID(product.ID),
		product.Handle,
		product.Title,
		product.Description,
		product.Vendor,
		product.CategoryID,
		string(product.Status),
	).Scan(&createdAt)
	if err != nil {
		return domain.Product{}, translateCreateError(err)
	}

	// 2. product_options + product_option_values.
	for _, opt := range product.Options {
		_, err = tx.Exec(ctx, `
			INSERT INTO product_options (id, product_id, name, position)
			VALUES ($1, $2, $3, $4)`,
			pgUUID(opt.ID), pgUUID(product.ID), opt.Name, opt.Position,
		)
		if err != nil {
			return domain.Product{}, err
		}

		for _, value := range opt.Values {
			_, err = tx.Exec(ctx, `
				INSERT INTO product_option_values (id, option_id, value, position)
				VALUES ($1, $2, $3, $4)`,
				pgUUID(value.ID), pgUUID(value.OptionID), value.Value, value.Position,
			)
			if err != nil {
				return domain.Product{}, err
			}
		}
	}

	// 3. product_media (gallery).
	for _, m := range product.Media {
		_, err = tx.Exec(ctx, `
			INSERT INTO product_media (id, product_id, type, url, alt_text, position)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			pgUUID(m.ID), pgUUID(product.ID), m.Type, m.URL, m.AltText, m.Position,
		)
		if err != nil {
			return domain.Product{}, err
		}
	}

	// 4. variants.
	for _, v := range product.Variants {
		_, err = tx.Exec(ctx, `
			INSERT INTO variants (id, product_id, sku, barcode, title, price, weight, options, is_deleted)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false)`,
			pgUUID(v.ID),
			pgUUID(product.ID),
			v.SKU,
			v.Barcode,
			v.Title,
			pgNumeric(v.Price),
			pgNumeric(v.Weight),
			v.Options,
		)
		if err != nil {
			return domain.Product{}, translateCreateError(err)
		}
	}

	// 5. inventory_items.
	for _, item := range params.InventoryItems {
		_, err = tx.Exec(ctx, `
			INSERT INTO inventory_items (id, variant_id, description, track_inventory)
			VALUES ($1, $2, $3, $4)`,
			pgUUID(item.ID),
			pgUUIDPtr(item.VariantID),
			item.Description,
			item.TrackInventory,
		)
		if err != nil {
			return domain.Product{}, err
		}
	}

	// 6. stock_moves (one ADJUST move per variant). ADJUST only touches the
	// from location, so to_location_id stays NULL.
	for _, move := range params.StockMoves {
		_, err = tx.Exec(ctx, `
			INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			pgUUID(move.ID),
			pgUUID(move.InventoryItemID),
			pgUUID(locationID),
			pgtype.UUID{}, // NULL
			string(move.MoveType),
			move.Quantity.Int(),
		)
		if err != nil {
			return domain.Product{}, err
		}
	}

	// 7. inventory_levels (initial level for the item/location pair).
	for _, level := range params.InventoryLevels {
		_, err = tx.Exec(ctx, `
			INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
			VALUES ($1, $2, $3, $4, $5)`,
			pgUUID(level.ID),
			pgUUID(level.InventoryItemID),
			pgUUID(locationID),
			level.AvailableQty.Int(),
			level.ReservedQty.Int(),
		)
		if err != nil {
			return domain.Product{}, err
		}
	}

	// 8. variant_media links.
	for _, v := range product.Variants {
		for _, vm := range v.Media {
			_, err = tx.Exec(ctx, `
				INSERT INTO variant_media (variant_id, media_id, position)
				VALUES ($1, $2, $3)`,
				pgUUID(vm.VariantID),
				pgUUID(vm.MediaID),
				vm.Position,
			)
			if err != nil {
				return domain.Product{}, err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Product{}, err
	}

	product.CreatedAt = createdAt
	// product.UpdatedAt = updatedAt

	return product, nil
}

// priceStatsJoin supplies each product's min/max variant price range as a
// single-row LATERAL join. It is shared by FindAll's count and data queries so
// the WHERE clause can reference price_stats.* columns.
const priceStatsJoin = `LEFT JOIN LATERAL (
		SELECT MIN(v.price) AS min_price, MAX(v.price) AS max_price
		FROM variants v
		WHERE v.product_id = products.id AND v.is_deleted = false
	) price_stats ON true`

// buildListConditions assembles the WHERE conditions (with $N placeholders),
// their bound arguments, and the total number of bound arguments for the
// product list query. Price filtering reuses the same price_stats join as the
// SELECT: minPrice only keeps products whose highest variant price is >=
// minPrice, maxPrice only keeps products whose lowest variant price is <=
// maxPrice, and both together keep products whose price range overlaps the
// requested window (inclusive on both ends). Sorting remains whitelisted by
// the caller.
func buildListConditions(params domain.ListProductParams) ([]string, []interface{}, int) {
	conditions := []string{}
	args := []interface{}{}
	argIdx := 0

	if params.Search != "" {
		argIdx++
		args = append(args, "%"+params.Search+"%")
		conditions = append(conditions, fmt.Sprintf(
			"(products.title ILIKE $%d OR products.handle ILIKE $%d OR category.name ILIKE $%d)",
			argIdx, argIdx, argIdx,
		))
	}

	if params.CategoryID > 0 {
		argIdx++
		args = append(args, params.CategoryID)
		conditions = append(conditions, fmt.Sprintf("products.category_id = $%d", argIdx))
	}

	if params.Status != "" {
		argIdx++
		args = append(args, params.Status)
		conditions = append(conditions, fmt.Sprintf("products.status = $%d", argIdx))
	}

	if params.MinPrice != nil {
		argIdx++
		args = append(args, pgNumeric(*params.MinPrice))
		conditions = append(conditions, fmt.Sprintf("price_stats.max_price >= $%d", argIdx))
	}

	if params.MaxPrice != nil {
		argIdx++
		args = append(args, pgNumeric(*params.MaxPrice))
		conditions = append(conditions, fmt.Sprintf("price_stats.min_price <= $%d", argIdx))
	}

	return conditions, args, argIdx
}

// FindAll returns a lightweight page of products (base columns plus the nested
// category reference and min/max variant prices, no nested options/variants/
// media) matching the given filter, plus the total count of matching rows
// before pagination.
func (r *productRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.ProductListItem, int, error) {

	defer metrics.ObserveDB("product", "find_all")(time.Now())

	conditions, args, argIdx := buildListConditions(params)

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	// total count matching filters (before pagination). The category and
	// price_stats joins are required because the search and price conditions
	// reference category.name and price_stats.* respectively.
	countQuery := "SELECT COUNT(*) FROM products LEFT JOIN category ON products.category_id = category.id " + priceStatsJoin + where
	var total int
	if err := r.db.GetDb().QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// whitelist ORDER BY (only allow-list values, never raw user input)
	sortBy := "products.created_at"
	if params.SortBy == "title" {
		sortBy = "products.title"
	} else if params.SortBy == "category_name" {
		sortBy = "category.name"
	}

	sortDir := "DESC"
	if params.SortDir == "asc" {
		sortDir = "ASC"
	}

	offset := (params.Page - 1) * params.PerPage

	query := fmt.Sprintf(`
		SELECT
			products.id,
			products.handle,
			products.title,
			products.status,
			products.description,
			products.vendor,
			products.category_id,
			products.created_at,
			products.updated_at,
			category.slug AS category_slug,
			category.name AS category_name,
			COALESCE(price_stats.min_price, 0) AS start_price,
			COALESCE(price_stats.max_price, 0) AS max_price,
			thumbnail.type AS thumbnail_type,
			thumbnail.url AS thumbnail_url,
			thumbnail.alt_text AS thumbnail_alt_text

		FROM products
		LEFT JOIN category ON products.category_id = category.id
		%s
		LEFT JOIN LATERAL (
			SELECT pm.type, pm.url, pm.alt_text
			FROM product_media pm
			WHERE pm.product_id = products.id AND pm.position = 1
			LIMIT 1
		) thumbnail ON true
		%s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d
	`, priceStatsJoin, where, sortBy, sortDir, argIdx+1, argIdx+2)

	args = append(args, params.PerPage, offset)

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	listRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[productListRow])
	if err != nil {
		return nil, 0, err
	}

	products := make([]domain.ProductListItem, 0, len(listRows))
	for _, row := range listRows {
		item := domain.ProductListItem{Product: row.Product.ToDomain()}
		if row.CategorySlug != nil {
			item.Category.Slug = *row.CategorySlug
		}
		if row.CategoryName != nil {
			item.Category.Name = *row.CategoryName
		}
		item.Prices.StartPrice = row.StartPrice
		item.Prices.MaxPrice = row.MaxPrice
		if row.ThumbnailURL != nil {
			item.Thumbnail = &domain.ProductThumbnail{
				Type:    *row.ThumbnailType,
				URL:     *row.ThumbnailURL,
				AltText: row.ThumbnailAltText,
			}
		}
		products = append(products, item)
	}

	return products, total, nil
}

// FindByID returns a fully hydrated product (options, variants, media) by UUID.
func (r *productRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.ProductDetail, error) {

	defer metrics.ObserveDB("product", "find_by_id")(time.Now())

	return r.findProductBy(ctx, "products.id = $1", pgUUID(id))
}

// FindByHandle returns a fully hydrated product (options, variants, media) by handle.
func (r *productRepo) FindByHandle(ctx context.Context, handle string) (domain.ProductDetail, error) {

	defer metrics.ObserveDB("product", "find_by_handle")(time.Now())

	return r.findProductBy(ctx, "products.handle = $1", handle)
}

// productHeaderColumns lists the columns returned by header-level product
// queries and updates. It must stay in sync with domain.Product's db tags.
const productHeaderColumns = "id, handle, title, status, description, vendor, category_id, created_at, updated_at"

// UpdateHeader performs a partial update of a product's header fields. Only
// the columns whose pointer fields in input are non-nil are touched; nil means
// "leave unchanged". If no fields are provided it returns the current row
// without writing anything.
func (r *productRepo) UpdateHeader(ctx context.Context, id uuid.UUID, input domain.UpdateProductInput) (domain.Product, error) {

	defer metrics.ObserveDB("product", "update_header")(time.Now())

	fields := make([]string, 0, 6)
	args := make([]interface{}, 0, 7)
	argIdx := 0

	if input.Title != nil {
		argIdx++
		args = append(args, *input.Title)
		fields = append(fields, fmt.Sprintf("title = $%d", argIdx))
	}
	if input.Description != nil {
		argIdx++
		args = append(args, *input.Description)
		fields = append(fields, fmt.Sprintf("description = $%d", argIdx))
	}
	if input.Vendor != nil {
		argIdx++
		args = append(args, *input.Vendor)
		fields = append(fields, fmt.Sprintf("vendor = $%d", argIdx))
	}
	if input.Handle != nil {
		argIdx++
		args = append(args, *input.Handle)
		fields = append(fields, fmt.Sprintf("handle = $%d", argIdx))
	}
	if input.CategoryID != nil {
		argIdx++
		args = append(args, *input.CategoryID)
		fields = append(fields, fmt.Sprintf("category_id = $%d", argIdx))
	}
	if input.Status != nil {
		argIdx++
		args = append(args, string(*input.Status))
		fields = append(fields, fmt.Sprintf("status = $%d", argIdx))
	}

	// No changed fields: return the current header state unchanged.
	if len(fields) == 0 {
		rows, err := r.db.GetDb().Query(ctx,
			"SELECT "+productHeaderColumns+" FROM products WHERE id = $1", pgUUID(id))
		if err != nil {
			return domain.Product{}, err
		}
		row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.Product])
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.Product{}, domain.ErrProductNotFound
			}
			return domain.Product{}, err
		}
		return row.ToDomain(), nil
	}

	// Always refresh updated_at on a real update.
	fields = append(fields, "updated_at = now()")

	argIdx++
	args = append(args, pgUUID(id))

	query := fmt.Sprintf(
		"UPDATE products SET %s WHERE id = $%d RETURNING "+productHeaderColumns,
		strings.Join(fields, ", "), argIdx,
	)

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return domain.Product{}, translateUpdateError(err)
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.Product])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, domain.ErrProductNotFound
		}
		return domain.Product{}, err
	}

	return row.ToDomain(), nil
}

// UpdateStatus writes a product's lifecycle status.
func (r *productRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.ProductStatus) error {

	defer metrics.ObserveDB("product", "update_status")(time.Now())

	tag, err := r.db.GetDb().Exec(ctx, `
		UPDATE products SET status = $1, updated_at = now() WHERE id = $2`,
		string(status), pgUUID(id),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrProductNotFound
	}

	return nil
}

// Delete hard-deletes (purges) a product and all of its related rows. It is
// guarded: a product whose variants have any stock_moves history cannot be
// purged and returns domain.ErrProductHasHistory instead.
func (r *productRepo) Delete(ctx context.Context, id uuid.UUID) error {

	defer metrics.ObserveDB("product", "purge")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Guard: block purge when any variant's inventory item has stock_moves.
	var historyCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM stock_moves sm
		JOIN inventory_items ii ON ii.id = sm.inventory_item_id
		JOIN variants v ON v.id = ii.variant_id
		WHERE v.product_id = $1`, pgUUID(id)).Scan(&historyCount)
	if err != nil {
		return err
	}
	if historyCount > 0 {
		return domain.ErrProductHasHistory
	}

	// Delete children in dependency order (no ON DELETE CASCADE is defined in
	// the schema, so we remove dependents explicitly).
	deletes := []string{
		`DELETE FROM variant_media WHERE variant_id IN (SELECT id FROM variants WHERE product_id = $1)`,
		`DELETE FROM inventory_levels WHERE inventory_item_id IN (SELECT ii.id FROM inventory_items ii JOIN variants v ON v.id = ii.variant_id WHERE v.product_id = $1)`,
		`DELETE FROM stock_moves WHERE inventory_item_id IN (SELECT ii.id FROM inventory_items ii JOIN variants v ON v.id = ii.variant_id WHERE v.product_id = $1)`,
		`DELETE FROM inventory_items WHERE variant_id IN (SELECT id FROM variants WHERE product_id = $1)`,
		`DELETE FROM variants WHERE product_id = $1`,
		`DELETE FROM product_option_values WHERE option_id IN (SELECT id FROM product_options WHERE product_id = $1)`,
		`DELETE FROM product_options WHERE product_id = $1`,
		`DELETE FROM product_media WHERE product_id = $1`,
	}

	for _, q := range deletes {
		if _, err = tx.Exec(ctx, q, pgUUID(id)); err != nil {
			return err
		}
	}

	tag, err := tx.Exec(ctx, `DELETE FROM products WHERE id = $1`, pgUUID(id))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrProductNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// CreateOption inserts an option row plus all of its value rows in one
// transaction.
func (r *productRepo) CreateOption(ctx context.Context, option domain.ProductOption) (domain.ProductOption, error) {

	defer metrics.ObserveDB("product", "create_option")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return domain.ProductOption{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO product_options (id, product_id, name, position)
		VALUES ($1, $2, $3, $4)`,
		pgUUID(option.ID), pgUUID(option.ProductID), option.Name, option.Position,
	)
	if err != nil {
		return domain.ProductOption{}, translateOptionCreateError(err)
	}

	for _, value := range option.Values {
		_, err = tx.Exec(ctx, `
			INSERT INTO product_option_values (id, option_id, value, position)
			VALUES ($1, $2, $3, $4)`,
			pgUUID(value.ID), pgUUID(value.OptionID), value.Value, value.Position,
		)
		if err != nil {
			return domain.ProductOption{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.ProductOption{}, err
	}

	return option, nil
}

// RenameOption renames an option, guarded by product_id so a mismatched
// product in the URL resolves to domain.ErrOptionNotFound.
func (r *productRepo) RenameOption(ctx context.Context, productID, optionID uuid.UUID, name string) (domain.ProductOption, error) {

	defer metrics.ObserveDB("product", "rename_option")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		UPDATE product_options
		SET name = $1
		WHERE id = $2 AND product_id = $3
		RETURNING id, product_id, name, position`,
		name, pgUUID(optionID), pgUUID(productID),
	)
	if err != nil {
		return domain.ProductOption{}, translateOptionCreateError(err)
	}

	option, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductOption])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOption{}, domain.ErrOptionNotFound
		}
		return domain.ProductOption{}, err
	}

	return option.ToDomain(), nil
}

// DeleteOption removes an option and all of its values in one transaction.
func (r *productRepo) DeleteOption(ctx context.Context, productID, optionID uuid.UUID) error {

	defer metrics.ObserveDB("product", "delete_option")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Guard by product_id so a mismatched product in the URL 404s.
	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM product_options WHERE id = $1 AND product_id = $2)`,
		pgUUID(optionID), pgUUID(productID),
	).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return domain.ErrOptionNotFound
	}

	if _, err = tx.Exec(ctx, `DELETE FROM product_option_values WHERE option_id = $1`, pgUUID(optionID)); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `DELETE FROM product_options WHERE id = $1`, pgUUID(optionID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOptionNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// ReorderOptions bulk-updates option positions in one transaction, guarded by
// product_id to prevent cross-product reorder abuse.
func (r *productRepo) ReorderOptions(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {

	defer metrics.ObserveDB("product", "reorder_options")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range positions {
		tag, err := tx.Exec(ctx, `
			UPDATE product_options SET position = $1
			WHERE id = $2 AND product_id = $3`,
			p.Position, pgUUID(p.ID), pgUUID(productID),
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrOptionNotFound
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// FindOptionByID loads a single product option by id.
func (r *productRepo) FindOptionByID(ctx context.Context, optionID uuid.UUID) (domain.ProductOption, error) {

	defer metrics.ObserveDB("product", "find_option_by_id")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		SELECT id, product_id, name, position
		FROM product_options
		WHERE id = $1`, pgUUID(optionID))
	if err != nil {
		return domain.ProductOption{}, err
	}

	option, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductOption])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOption{}, domain.ErrOptionNotFound
		}
		return domain.ProductOption{}, err
	}

	return option.ToDomain(), nil
}

// CreateOptionValue inserts a single option value row.
func (r *productRepo) CreateOptionValue(ctx context.Context, value domain.ProductOptionValue) (domain.ProductOptionValue, error) {

	defer metrics.ObserveDB("product", "create_option_value")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		INSERT INTO product_option_values (id, option_id, value, position)
		VALUES ($1, $2, $3, $4)
		RETURNING id, option_id, value, position`,
		pgUUID(value.ID), pgUUID(value.OptionID), value.Value, value.Position,
	)
	if err != nil {
		return domain.ProductOptionValue{}, translateOptionValueCreateError(err)
	}

	created, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductOptionValue])
	if err != nil {
		return domain.ProductOptionValue{}, err
	}

	return created.ToDomain(), nil
}

// UpdateOptionValue partially updates an option value, touching only the
// non-nil fields.
func (r *productRepo) UpdateOptionValue(ctx context.Context, valueID uuid.UUID, value *string, position *int) (domain.ProductOptionValue, error) {

	defer metrics.ObserveDB("product", "update_option_value")(time.Now())

	const valueColumns = "id, option_id, value, position"

	fields := make([]string, 0, 2)
	args := make([]interface{}, 0, 3)
	argIdx := 0

	if value != nil {
		argIdx++
		args = append(args, *value)
		fields = append(fields, fmt.Sprintf("value = $%d", argIdx))
	}
	if position != nil {
		argIdx++
		args = append(args, *position)
		fields = append(fields, fmt.Sprintf("position = $%d", argIdx))
	}

	// No changed fields: return the current row unchanged.
	if len(fields) == 0 {
		rows, err := r.db.GetDb().Query(ctx,
			"SELECT "+valueColumns+" FROM product_option_values WHERE id = $1", pgUUID(valueID))
		if err != nil {
			return domain.ProductOptionValue{}, err
		}
		got, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductOptionValue])
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ProductOptionValue{}, domain.ErrOptionValueNotFound
			}
			return domain.ProductOptionValue{}, err
		}
		return got.ToDomain(), nil
	}

	argIdx++
	args = append(args, pgUUID(valueID))

	query := fmt.Sprintf(
		"UPDATE product_option_values SET %s WHERE id = $%d RETURNING "+valueColumns,
		strings.Join(fields, ", "), argIdx,
	)

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return domain.ProductOptionValue{}, err
	}

	got, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductOptionValue])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOptionValue{}, domain.ErrOptionValueNotFound
		}
		return domain.ProductOptionValue{}, err
	}

	return got.ToDomain(), nil
}

// DeleteOptionValue removes a single option value row.
func (r *productRepo) DeleteOptionValue(ctx context.Context, valueID uuid.UUID) error {

	defer metrics.ObserveDB("product", "delete_option_value")(time.Now())

	tag, err := r.db.GetDb().Exec(ctx, `DELETE FROM product_option_values WHERE id = $1`, pgUUID(valueID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOptionValueNotFound
	}

	return nil
}

// FindOptionValueByID loads a single product option value by id.
func (r *productRepo) FindOptionValueByID(ctx context.Context, valueID uuid.UUID) (domain.ProductOptionValue, error) {

	defer metrics.ObserveDB("product", "find_option_value_by_id")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		SELECT id, option_id, value, position
		FROM product_option_values
		WHERE id = $1`, pgUUID(valueID))
	if err != nil {
		return domain.ProductOptionValue{}, err
	}

	got, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductOptionValue])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOptionValue{}, domain.ErrOptionValueNotFound
		}
		return domain.ProductOptionValue{}, err
	}

	return got.ToDomain(), nil
}

// findProductBy loads a single products row (with its nested category
// reference) plus its nested options, variants (with per-variant stock) and
// media using separate read queries (no transaction needed for reads).
func (r *productRepo) findProductBy(ctx context.Context, predicate string, arg interface{}) (domain.ProductDetail, error) {

	query := fmt.Sprintf(`
		SELECT
			products.id,
			products.handle,
			products.title,
			products.status,
			products.description,
			products.vendor,
			products.category_id,
			products.created_at,
			products.updated_at,
			category.slug AS category_slug,
			category.name AS category_name
		FROM products
		LEFT JOIN category ON products.category_id = category.id
		WHERE %s`, predicate)

	rows, err := r.db.GetDb().Query(ctx, query, arg)
	if err != nil {
		return domain.ProductDetail{}, err
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[productDetailRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductDetail{}, domain.ErrProductNotFound
		}
		return domain.ProductDetail{}, err
	}

	detail := domain.ProductDetail{Product: row.Product.ToDomain()}
	if row.CategorySlug != nil {
		detail.Category.Slug = *row.CategorySlug
	}
	if row.CategoryName != nil {
		detail.Category.Name = *row.CategoryName
	}
	if row.CategoryId != nil {
		detail.Category.Id = *row.CategoryId
	}

	options, err := r.findProductOptions(ctx, detail.ID)
	if err != nil {
		return domain.ProductDetail{}, err
	}
	detail.Options = options

	variants, err := r.findProductVariants(ctx, detail.ID)
	if err != nil {
		return domain.ProductDetail{}, err
	}
	detail.Variants = variants

	media, err := r.findProductMedia(ctx, detail.ID)
	if err != nil {
		return domain.ProductDetail{}, err
	}
	detail.Media = media

	return detail, nil
}

// findProductOptions loads product_options plus their nested option values.
func (r *productRepo) findProductOptions(ctx context.Context, productID uuid.UUID) ([]domain.ProductOption, error) {

	optionRows, err := r.db.GetDb().Query(ctx, `
		SELECT id, product_id, name, position
		FROM product_options
		WHERE product_id = $1
		ORDER BY position ASC, id ASC`, pgUUID(productID))
	if err != nil {
		return nil, err
	}
	defer optionRows.Close()

	optionModels, err := pgx.CollectRows(optionRows, pgx.RowToStructByName[model.ProductOption])
	if err != nil {
		return nil, err
	}
	options := make([]domain.ProductOption, 0, len(optionModels))
	for _, m := range optionModels {
		options = append(options, m.ToDomain())
	}
	if len(options) == 0 {
		return options, nil
	}

	valueRows, err := r.db.GetDb().Query(ctx, `
		SELECT pov.id, pov.option_id, pov.value, pov.position
		FROM product_option_values pov
		JOIN product_options po ON po.id = pov.option_id
		WHERE po.product_id = $1
		ORDER BY po.position ASC, pov.position ASC, pov.id ASC`, pgUUID(productID))
	if err != nil {
		return nil, err
	}
	defer valueRows.Close()

	valueModels, err := pgx.CollectRows(valueRows, pgx.RowToStructByName[model.ProductOptionValue])
	if err != nil {
		return nil, err
	}
	values := make([]domain.ProductOptionValue, 0, len(valueModels))
	for _, m := range valueModels {
		values = append(values, m.ToDomain())
	}

	valuesByOption := make(map[uuid.UUID][]domain.ProductOptionValue, len(options))
	for _, v := range values {
		valuesByOption[v.OptionID] = append(valuesByOption[v.OptionID], v)
	}

	for i := range options {
		options[i].Values = valuesByOption[options[i].ID]
		if options[i].Values == nil {
			options[i].Values = []domain.ProductOptionValue{}
		}
	}

	return options, nil
}

// findProductVariants loads non-deleted variants, their per-variant available
// stock (summed across all inventory levels), and their variant_media links.
func (r *productRepo) findProductVariants(ctx context.Context, productID uuid.UUID) ([]domain.Variant, error) {

	variantRows, err := r.db.GetDb().Query(ctx, `
		SELECT
			v.id,
			v.product_id,
			v.sku,
			v.barcode,
			v.title,
			v.price,
			v.weight,
			v.position,
			v.options,
			v.is_deleted,
			v.created_at,
			v.updated_at,
			COALESCE((
				SELECT SUM(il.available_qty)
				FROM inventory_items ii
				JOIN inventory_levels il ON il.inventory_item_id = ii.id
				WHERE ii.variant_id = v.id
			), 0) AS stock
		FROM variants v
		WHERE v.product_id = $1 AND v.is_deleted = false
		ORDER BY v.position ASC, v.created_at ASC, v.id ASC`, pgUUID(productID))
	if err != nil {
		return nil, err
	}
	defer variantRows.Close()

	variantModels, err := pgx.CollectRows(variantRows, pgx.RowToStructByName[model.Variant])
	if err != nil {
		return nil, err
	}
	variants := make([]domain.Variant, 0, len(variantModels))
	for _, m := range variantModels {
		variants = append(variants, m.ToDomain())
	}
	if len(variants) == 0 {
		return variants, nil
	}

	mediaRows, err := r.db.GetDb().Query(ctx, `
		SELECT vm.variant_id, vm.media_id, vm.position
		FROM variant_media vm
		JOIN variants v ON v.id = vm.variant_id
		WHERE v.product_id = $1 AND v.is_deleted = false
		ORDER BY vm.position ASC`, pgUUID(productID))
	if err != nil {
		return nil, err
	}
	defer mediaRows.Close()

	mediaLinkModels, err := pgx.CollectRows(mediaRows, pgx.RowToStructByName[model.VariantMedia])
	if err != nil {
		return nil, err
	}
	mediaLinks := make([]domain.VariantMedia, 0, len(mediaLinkModels))
	for _, m := range mediaLinkModels {
		mediaLinks = append(mediaLinks, m.ToDomain())
	}

	mediaByVariant := make(map[uuid.UUID][]domain.VariantMedia, len(variants))
	for _, m := range mediaLinks {
		mediaByVariant[m.VariantID] = append(mediaByVariant[m.VariantID], m)
	}

	for i := range variants {
		variants[i].Media = mediaByVariant[variants[i].ID]
		if variants[i].Media == nil {
			variants[i].Media = []domain.VariantMedia{}
		}
	}

	return variants, nil
}

// findProductMedia loads the product's gallery media.
func (r *productRepo) findProductMedia(ctx context.Context, productID uuid.UUID) ([]domain.ProductMedia, error) {

	rows, err := r.db.GetDb().Query(ctx, `
		SELECT id, product_id, type, url, alt_text, position, created_at, updated_at
		FROM product_media
		WHERE product_id = $1
		ORDER BY position ASC, id ASC`, pgUUID(productID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mediaModels, err := pgx.CollectRows(rows, pgx.RowToStructByName[model.ProductMedia])
	if err != nil {
		return nil, err
	}
	media := make([]domain.ProductMedia, 0, len(mediaModels))
	for _, m := range mediaModels {
		media = append(media, m.ToDomain())
	}

	return media, nil
}

// productListRow is the row shape scanned by FindAll: base product columns
// (via the embedded model.Product) plus the joined category slug/name and the
// computed min/max variant prices. Nested domain fields (Category/Prices) are
// populated manually after scanning.
type productListRow struct {
	model.Product
	CategorySlug     *string         `db:"category_slug"`
	CategoryName     *string         `db:"category_name"`
	StartPrice       decimal.Decimal `db:"start_price"`
	MaxPrice         decimal.Decimal `db:"max_price"`
	ThumbnailType    *string         `db:"thumbnail_type"`
	ThumbnailURL     *string         `db:"thumbnail_url"`
	ThumbnailAltText *string         `db:"thumbnail_alt_text"`
}

// productDetailRow is the row shape scanned by findProductBy: base product
// columns plus the joined category slug/name.
type productDetailRow struct {
	model.Product
	CategoryId   *int    `db:"category_id"`
	CategorySlug *string `db:"category_slug"`
	CategoryName *string `db:"category_name"`
}

// pgUUID encodes a google/uuid.UUID as pgx's pgtype.UUID.
func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// pgUUIDPtr encodes a nullable google/uuid.UUID as pgx's pgtype.UUID.
func pgUUIDPtr(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// pgNumeric encodes a shopspring decimal as pgx's pgtype.Numeric.
func pgNumeric(d decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{Int: d.Coefficient(), Exp: d.Exponent(), Valid: true}
}

// translateCreateError maps PostgreSQL constraint violations to domain errors.
func translateCreateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return domain.ErrSKUAlreadyExists
	}
	return err
}

// translateUpdateError maps constraint violations from a product header update
// to domain errors. The only unique constraint reachable on products is handle.
func translateUpdateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return domain.ErrProductHandleAlreadyExists
	}
	return err
}

// translateOptionCreateError maps constraint violations from option writes to
// domain errors. The (product_id, name) unique index is the only reachable one.
func translateOptionCreateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return domain.ErrOptionAlreadyExists
	}
	return err
}

// translateOptionValueCreateError maps constraint violations from option value
// writes to domain errors. The only foreign key on product_option_values is
// option_id, so a violation means the option does not exist.
func translateOptionValueCreateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return domain.ErrOptionNotFound
	}
	return err
}

// variantColumns lists the base columns returned by standalone variant queries.
// It must stay in sync with domain.Variant's db tags. The computed stock column
// is added separately where the full detail shape is needed.
const variantColumns = "id, product_id, sku, barcode, title, price, weight, position, options, is_deleted, created_at, updated_at"

// CreateVariant inserts a single variant row.
func (r *productRepo) CreateVariant(ctx context.Context, variant domain.Variant) (domain.Variant, error) {

	defer metrics.ObserveDB("product", "create_variant")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		INSERT INTO variants (id, product_id, sku, barcode, title, price, weight, position, options, is_deleted)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, false)
		RETURNING `+variantColumns,
		pgUUID(variant.ID),
		pgUUID(variant.ProductID),
		variant.SKU,
		variant.Barcode,
		variant.Title,
		pgNumeric(variant.Price),
		pgNumeric(variant.Weight),
		variant.Position,
		variant.Options,
	)
	if err != nil {
		return domain.Variant{}, translateCreateError(err)
	}

	created, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[model.Variant])
	if err != nil {
		return domain.Variant{}, err
	}

	return created.ToDomain(), nil
}

// CreateVariants bulk-inserts variants using a single multi-row INSERT.
func (r *productRepo) CreateVariants(ctx context.Context, variants []domain.Variant) ([]domain.Variant, error) {

	defer metrics.ObserveDB("product", "create_variants")(time.Now())

	if len(variants) == 0 {
		return []domain.Variant{}, nil
	}

	placeholders := make([]string, 0, len(variants))
	args := make([]interface{}, 0, len(variants)*9)
	for _, v := range variants {
		n := len(args)
		placeholders = append(placeholders, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, false)",
			n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8, n+9,
		))
		args = append(args,
			pgUUID(v.ID),
			pgUUID(v.ProductID),
			v.SKU,
			v.Barcode,
			v.Title,
			pgNumeric(v.Price),
			pgNumeric(v.Weight),
			v.Position,
			v.Options,
		)
	}

	query := fmt.Sprintf(`
		INSERT INTO variants (id, product_id, sku, barcode, title, price, weight, position, options, is_deleted)
		VALUES %s
		RETURNING `+variantColumns, strings.Join(placeholders, ", "))

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return nil, translateCreateError(err)
	}

	createdModels, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[model.Variant])
	if err != nil {
		return nil, err
	}
	created := make([]domain.Variant, 0, len(createdModels))
	for _, m := range createdModels {
		created = append(created, m.ToDomain())
	}

	// RETURNING order is not guaranteed for multi-row inserts, so reorder the
	// result to match the input slice.
	byID := make(map[uuid.UUID]domain.Variant, len(created))
	for _, v := range created {
		byID[v.ID] = v
	}
	ordered := make([]domain.Variant, 0, len(variants))
	for _, v := range variants {
		ordered = append(ordered, byID[v.ID])
	}

	return ordered, nil
}

// CreateVariantsWithStock persists one or more new variants together with their
// inventory items, initial ADJUST stock moves, inventory levels and media links
// inside a single transaction. It mirrors the variant-writing portion of Create.
func (r *productRepo) CreateVariantsWithStock(ctx context.Context, params domain.CreateVariantsParams) ([]domain.Variant, error) {

	defer metrics.ObserveDB("product", "create_variants_with_stock")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		fmt.Println("DEBUG Repo CreateVariantsWithStock 1", err.Error())
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Resolve the default location once per request, not per variant.
	var locationUUID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM locations WHERE is_default = true LIMIT 1`).Scan(&locationUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 2", err.Error())
			return nil, domain.ErrDefaultLocationNotFound
		}
		fmt.Println("DEBUG Repo CreateVariantsWithStock 3", err.Error())
		return nil, err
	}
	locationID := uuid.UUID(locationUUID.Bytes)

	// 1. Brand-new product_media rows.
	for _, m := range params.NewMedia {
		_, err = tx.Exec(ctx, `
			INSERT INTO product_media (id, product_id, type, url, alt_text, position)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			pgUUID(m.ID), pgUUID(m.ProductID), m.Type, m.URL, m.AltText, m.Position,
		)
		if err != nil {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 4", err.Error())
			return nil, err
		}
	}

	// 2. variants, using RETURNING so callers get back real created_at/etc.
	createdVariants := make([]domain.Variant, 0, len(params.Variants))
	if len(params.Variants) > 0 {
		placeholders := make([]string, 0, len(params.Variants))
		args := make([]interface{}, 0, len(params.Variants)*9)
		for _, v := range params.Variants {
			n := len(args)
			placeholders = append(placeholders, fmt.Sprintf(
				"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, false)",
				n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8, n+9,
			))
			args = append(args,
				pgUUID(v.ID),
				pgUUID(v.ProductID),
				v.SKU,
				v.Barcode,
				v.Title,
				pgNumeric(v.Price),
				pgNumeric(v.Weight),
				v.Position,
				v.Options,
			)
		}

		query := fmt.Sprintf(`
			INSERT INTO variants (id, product_id, sku, barcode, title, price, weight, position, options, is_deleted)
			VALUES %s
			RETURNING `+variantColumns, strings.Join(placeholders, ", "))

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 5", err.Error())
			return nil, translateCreateError(err)
		}

		createdModels, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[model.Variant])
		if err != nil {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 6", err.Error())
			return nil, err
		}
		created := make([]domain.Variant, 0, len(createdModels))
		for _, m := range createdModels {
			created = append(created, m.ToDomain())
		}

		// RETURNING order is not guaranteed for multi-row inserts, so reorder
		// the result to match the input slice.
		byID := make(map[uuid.UUID]domain.Variant, len(created))
		for _, v := range created {
			byID[v.ID] = v
		}
		for _, v := range params.Variants {
			createdVariants = append(createdVariants, byID[v.ID])
		}
	}

	// 3. inventory_items.
	for _, item := range params.InventoryItems {
		_, err = tx.Exec(ctx, `
			INSERT INTO inventory_items (id, variant_id, description, track_inventory)
			VALUES ($1, $2, $3, $4)`,
			pgUUID(item.ID),
			pgUUIDPtr(item.VariantID),
			item.Description,
			item.TrackInventory,
		)
		if err != nil {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 7", err.Error())
			return nil, err
		}
	}

	// 4. stock_moves (one ADJUST move per variant). ADJUST only touches the
	// from location, so to_location_id stays NULL.
	for _, move := range params.StockMoves {
		_, err = tx.Exec(ctx, `
			INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			pgUUID(move.ID),
			pgUUID(move.InventoryItemID),
			pgUUID(locationID),
			pgtype.UUID{}, // NULL
			string(move.MoveType),
			move.Quantity.Int(),
		)
		if err != nil {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 8", err.Error())
			return nil, err
		}
	}

	// 5. inventory_levels (initial level for the item/location pair).
	for _, level := range params.InventoryLevels {
		_, err = tx.Exec(ctx, `
			INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
			VALUES ($1, $2, $3, $4, $5)`,
			pgUUID(level.ID),
			pgUUID(level.InventoryItemID),
			pgUUID(locationID),
			level.AvailableQty.Int(),
			level.ReservedQty.Int(),
		)
		if err != nil {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 9", err.Error())
			return nil, err
		}
	}

	// 6. variant_media links.
	for _, vm := range params.VariantMedia {
		_, err = tx.Exec(ctx, `
			INSERT INTO variant_media (variant_id, media_id, position)
			VALUES ($1, $2, $3)`,
			pgUUID(vm.VariantID),
			pgUUID(vm.MediaID),
			vm.Position,
		)
		if err != nil {
			fmt.Println("DEBUG Repo CreateVariantsWithStock 10", err.Error())
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		fmt.Println("DEBUG Repo CreateVariantsWithStock 11", err.Error())
		return nil, err
	}

	fmt.Println("DEBUG Repo CreateVariantsWithStock END")

	return createdVariants, nil
}

// AdjustVariantStock records an ADJUST stock move that sets a variant's
// available quantity at the default location to the given absolute target,
// upserting the inventory_levels row for that item/location pair.
func (r *productRepo) AdjustVariantStock(ctx context.Context, variantID uuid.UUID, targetQty domain.Quantity) (domain.InventoryLevel, error) {

	defer metrics.ObserveDB("product", "adjust_variant_stock")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return domain.InventoryLevel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Resolve the default location.
	var locationUUID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM locations WHERE is_default = true LIMIT 1`).Scan(&locationUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryLevel{}, domain.ErrDefaultLocationNotFound
		}
		return domain.InventoryLevel{}, err
	}
	locationID := uuid.UUID(locationUUID.Bytes)

	// Look up the variant's inventory item.
	var inventoryItemID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM inventory_items WHERE variant_id = $1`, pgUUID(variantID)).Scan(&inventoryItemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryLevel{}, domain.ErrVariantNotFound
		}
		return domain.InventoryLevel{}, err
	}
	itemID := uuid.UUID(inventoryItemID.Bytes)

	// Current available quantity (0 when no level row exists yet).
	var currentQty int
	err = tx.QueryRow(ctx, `
		SELECT available_qty
		FROM inventory_levels
		WHERE inventory_item_id = $1 AND location_id = $2`,
		pgUUID(itemID), pgUUID(locationID),
	).Scan(&currentQty)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.InventoryLevel{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		currentQty = 0
	}

	delta := targetQty.Int() - currentQty

	// Record the ADJUST stock move (from the default location).
	_, err = tx.Exec(ctx, `
		INSERT INTO stock_moves (id, inventory_item_id, from_location_id, to_location_id, move_type, quantity)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		pgUUID(uuid.Must(uuid.NewV7())),
		pgUUID(itemID),
		pgUUID(locationID),
		pgtype.UUID{}, // NULL
		string(domain.StockMoveAdjust),
		delta,
	)
	if err != nil {
		return domain.InventoryLevel{}, err
	}

	// Upsert the inventory level to the absolute target. Relies on the unique
	// (inventory_item_id, location_id) index (see migrations/0002_...).
	rows, err := tx.Query(ctx, `
		INSERT INTO inventory_levels (id, inventory_item_id, location_id, available_qty, reserved_qty)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (inventory_item_id, location_id) DO UPDATE
		SET available_qty = EXCLUDED.available_qty, updated_at = now()
		RETURNING id, inventory_item_id, location_id, available_qty, reserved_qty, COALESCE(updated_at, now()) AS updated_at`,
		pgUUID(uuid.Must(uuid.NewV7())),
		pgUUID(itemID),
		pgUUID(locationID),
		targetQty.Int(),
		0,
	)
	if err != nil {
		return domain.InventoryLevel{}, err
	}

	level, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.InventoryLevel])
	if err != nil {
		return domain.InventoryLevel{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.InventoryLevel{}, err
	}

	return level.ToDomain(), nil
}

// FindVariantByID loads a single variant (including its computed stock) by id.
func (r *productRepo) FindVariantByID(ctx context.Context, variantID uuid.UUID) (domain.Variant, error) {

	defer metrics.ObserveDB("product", "find_variant_by_id")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		SELECT
			v.id,
			v.product_id,
			v.sku,
			v.barcode,
			v.title,
			v.price,
			v.weight,
			v.position,
			v.options,
			v.is_deleted,
			v.created_at,
			v.updated_at,
			COALESCE((
				SELECT SUM(il.available_qty)
				FROM inventory_items ii
				JOIN inventory_levels il ON il.inventory_item_id = ii.id
				WHERE ii.variant_id = v.id
			), 0) AS stock
		FROM variants v
		WHERE v.id = $1`, pgUUID(variantID))
	if err != nil {
		return domain.Variant{}, err
	}

	variant, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.Variant])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Variant{}, domain.ErrVariantNotFound
		}
		return domain.Variant{}, err
	}

	return variant.ToDomain(), nil
}

// UpdateVariant partially updates a variant, touching only the non-nil fields.
func (r *productRepo) UpdateVariant(ctx context.Context, variantID uuid.UUID, input domain.UpdateVariantInput) (domain.Variant, error) {

	defer metrics.ObserveDB("product", "update_variant")(time.Now())

	fields := make([]string, 0, 5)
	args := make([]interface{}, 0, 6)
	argIdx := 0

	if input.SKU != nil {
		argIdx++
		args = append(args, *input.SKU)
		fields = append(fields, fmt.Sprintf("sku = $%d", argIdx))
	}
	if input.Barcode != nil {
		argIdx++
		args = append(args, *input.Barcode)
		fields = append(fields, fmt.Sprintf("barcode = $%d", argIdx))
	}
	if input.Title != nil {
		argIdx++
		args = append(args, *input.Title)
		fields = append(fields, fmt.Sprintf("title = $%d", argIdx))
	}
	if input.Price != nil {
		argIdx++
		args = append(args, pgNumeric(*input.Price))
		fields = append(fields, fmt.Sprintf("price = $%d", argIdx))
	}
	if input.Weight != nil {
		argIdx++
		args = append(args, pgNumeric(*input.Weight))
		fields = append(fields, fmt.Sprintf("weight = $%d", argIdx))
	}

	// No changed fields: return the current row unchanged.
	if len(fields) == 0 {
		rows, err := r.db.GetDb().Query(ctx,
			"SELECT "+variantColumns+" FROM variants WHERE id = $1", pgUUID(variantID))
		if err != nil {
			return domain.Variant{}, err
		}
		variant, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[model.Variant])
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.Variant{}, domain.ErrVariantNotFound
			}
			return domain.Variant{}, err
		}
		return variant.ToDomain(), nil
	}

	fields = append(fields, "updated_at = now()")

	argIdx++
	args = append(args, pgUUID(variantID))

	query := fmt.Sprintf(
		"UPDATE variants SET %s WHERE id = $%d RETURNING "+variantColumns,
		strings.Join(fields, ", "), argIdx,
	)

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return domain.Variant{}, translateCreateError(err)
	}

	variant, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[model.Variant])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Variant{}, domain.ErrVariantNotFound
		}
		return domain.Variant{}, err
	}

	return variant.ToDomain(), nil
}

// DeleteVariant soft-deletes (is_deleted = true) or hard-deletes a variant,
// cascading its dependents in the hard-delete case.
func (r *productRepo) DeleteVariant(ctx context.Context, variantID uuid.UUID, hard bool) error {

	defer metrics.ObserveDB("product", "delete_variant")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tag pgconn.CommandTag
	if hard {
		// Cascade delete dependents in dependency order (no ON DELETE CASCADE
		// is defined in the schema).
		if _, err = tx.Exec(ctx, `DELETE FROM variant_media WHERE variant_id = $1`, pgUUID(variantID)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM inventory_levels WHERE inventory_item_id IN (SELECT id FROM inventory_items WHERE variant_id = $1)`, pgUUID(variantID)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM stock_moves WHERE inventory_item_id IN (SELECT id FROM inventory_items WHERE variant_id = $1)`, pgUUID(variantID)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM inventory_items WHERE variant_id = $1`, pgUUID(variantID)); err != nil {
			return err
		}
		tag, err = tx.Exec(ctx, `DELETE FROM variants WHERE id = $1`, pgUUID(variantID))
		if err != nil {
			return err
		}
	} else {
		tag, err = tx.Exec(ctx, `UPDATE variants SET is_deleted = true, updated_at = now() WHERE id = $1`, pgUUID(variantID))
		if err != nil {
			return err
		}
	}

	if tag.RowsAffected() == 0 {
		return domain.ErrVariantNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// BulkDeleteVariants soft- or hard-deletes many variants in one transaction.
func (r *productRepo) BulkDeleteVariants(ctx context.Context, variantIDs []uuid.UUID, hard bool) error {

	defer metrics.ObserveDB("product", "bulk_delete_variants")(time.Now())

	if len(variantIDs) == 0 {
		return nil
	}

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	args := make([]interface{}, 0, len(variantIDs))
	placeholders := make([]string, 0, len(variantIDs))
	for i, id := range variantIDs {
		args = append(args, pgUUID(id))
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	in := strings.Join(placeholders, ", ")

	if hard {
		if _, err = tx.Exec(ctx, `DELETE FROM variant_media WHERE variant_id IN (`+in+`)`, args...); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM inventory_levels WHERE inventory_item_id IN (SELECT id FROM inventory_items WHERE variant_id IN (`+in+`))`, args...); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM stock_moves WHERE inventory_item_id IN (SELECT id FROM inventory_items WHERE variant_id IN (`+in+`))`, args...); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM inventory_items WHERE variant_id IN (`+in+`)`, args...); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM variants WHERE id IN (`+in+`)`, args...); err != nil {
			return err
		}
	} else {
		if _, err = tx.Exec(ctx, `UPDATE variants SET is_deleted = true, updated_at = now() WHERE id IN (`+in+`)`, args...); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// RestoreVariant un-soft-deletes a variant.
func (r *productRepo) RestoreVariant(ctx context.Context, variantID uuid.UUID) (domain.Variant, error) {

	defer metrics.ObserveDB("product", "restore_variant")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		UPDATE variants SET is_deleted = false, updated_at = now()
		WHERE id = $1
		RETURNING `+variantColumns, pgUUID(variantID))
	if err != nil {
		return domain.Variant{}, err
	}

	variant, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[model.Variant])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Variant{}, domain.ErrVariantNotFound
		}
		return domain.Variant{}, err
	}

	return variant.ToDomain(), nil
}

// ReorderVariants bulk-updates variant positions in one transaction, guarded by
// product_id to prevent cross-product reorder abuse.
func (r *productRepo) ReorderVariants(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {

	defer metrics.ObserveDB("product", "reorder_variants")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range positions {
		tag, err := tx.Exec(ctx, `
			UPDATE variants SET position = $1
			WHERE id = $2 AND product_id = $3`,
			p.Position, pgUUID(p.ID), pgUUID(productID),
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrVariantNotFound
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// VariantHasHistory reports whether a variant has any stock movement history
// (which forces soft deletion over hard deletion).
func (r *productRepo) VariantHasHistory(ctx context.Context, variantID uuid.UUID) (bool, error) {

	defer metrics.ObserveDB("product", "variant_has_history")(time.Now())

	var exists bool
	err := r.db.GetDb().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM stock_moves sm
			JOIN inventory_items ii ON ii.id = sm.inventory_item_id
			WHERE ii.variant_id = $1
		)`, pgUUID(variantID)).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// productMediaColumns lists the columns returned by product_media queries. It
// must stay in sync with domain.ProductMedia's db tags.
const productMediaColumns = "id, product_id, type, url, alt_text, position, created_at, updated_at"

// CreateProductMedia bulk-inserts product gallery media rows.
func (r *productRepo) CreateProductMedia(ctx context.Context, media []domain.ProductMedia) ([]domain.ProductMedia, error) {

	defer metrics.ObserveDB("product", "create_product_media")(time.Now())

	if len(media) == 0 {
		return []domain.ProductMedia{}, nil
	}

	placeholders := make([]string, 0, len(media))
	args := make([]interface{}, 0, len(media)*6)
	for _, m := range media {
		n := len(args)
		placeholders = append(placeholders, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d)",
			n+1, n+2, n+3, n+4, n+5, n+6,
		))
		args = append(args,
			pgUUID(m.ID),
			pgUUID(m.ProductID),
			m.Type,
			m.URL,
			m.AltText,
			m.Position,
		)
	}

	query := fmt.Sprintf(`
		INSERT INTO product_media (id, product_id, type, url, alt_text, position)
		VALUES %s
		RETURNING `+productMediaColumns, strings.Join(placeholders, ", "))

	rows, err := r.db.GetDb().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	createdModels, err := pgx.CollectRows(rows, pgx.RowToStructByName[model.ProductMedia])
	if err != nil {
		return nil, err
	}
	created := make([]domain.ProductMedia, 0, len(createdModels))
	for _, m := range createdModels {
		created = append(created, m.ToDomain())
	}

	// Reorder results to match input order (RETURNING order is not guaranteed).
	byID := make(map[uuid.UUID]domain.ProductMedia, len(created))
	for _, m := range created {
		byID[m.ID] = m
	}
	ordered := make([]domain.ProductMedia, 0, len(media))
	for _, m := range media {
		ordered = append(ordered, byID[m.ID])
	}

	return ordered, nil
}

// UpdateProductMedia updates a media row's metadata (currently only alt_text).
func (r *productRepo) UpdateProductMedia(ctx context.Context, mediaID uuid.UUID, altText *string) (domain.ProductMedia, error) {

	defer metrics.ObserveDB("product", "update_product_media")(time.Now())

	if altText == nil {
		rows, err := r.db.GetDb().Query(ctx,
			"SELECT "+productMediaColumns+" FROM product_media WHERE id = $1", pgUUID(mediaID))
		if err != nil {
			return domain.ProductMedia{}, err
		}
		media, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductMedia])
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ProductMedia{}, domain.ErrMediaNotFound
			}
			return domain.ProductMedia{}, err
		}
		return media.ToDomain(), nil
	}

	rows, err := r.db.GetDb().Query(ctx, `
		UPDATE product_media SET alt_text = $1, updated_at = now()
		WHERE id = $2
		RETURNING `+productMediaColumns, altText, pgUUID(mediaID))
	if err != nil {
		return domain.ProductMedia{}, err
	}

	media, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductMedia])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductMedia{}, domain.ErrMediaNotFound
		}
		return domain.ProductMedia{}, err
	}

	return media.ToDomain(), nil
}

// DeleteProductMedia hard-deletes a media row and cascades its variant_media
// links in one transaction.
func (r *productRepo) DeleteProductMedia(ctx context.Context, mediaID uuid.UUID) error {

	defer metrics.ObserveDB("product", "delete_product_media")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, `DELETE FROM variant_media WHERE media_id = $1`, pgUUID(mediaID)); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `DELETE FROM product_media WHERE id = $1`, pgUUID(mediaID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMediaNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// ReorderProductMedia bulk-updates product gallery media positions in one
// transaction, guarded by product_id.
func (r *productRepo) ReorderProductMedia(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) error {

	defer metrics.ObserveDB("product", "reorder_product_media")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range positions {
		tag, err := tx.Exec(ctx, `
			UPDATE product_media SET position = $1
			WHERE id = $2 AND product_id = $3`,
			p.Position, pgUUID(p.ID), pgUUID(productID),
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrMediaNotFound
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// FindMediaByID loads a single product media row by id.
func (r *productRepo) FindMediaByID(ctx context.Context, mediaID uuid.UUID) (domain.ProductMedia, error) {

	defer metrics.ObserveDB("product", "find_media_by_id")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		SELECT `+productMediaColumns+`
		FROM product_media
		WHERE id = $1`, pgUUID(mediaID))
	if err != nil {
		return domain.ProductMedia{}, err
	}

	media, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.ProductMedia])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductMedia{}, domain.ErrMediaNotFound
		}
		return domain.ProductMedia{}, err
	}

	return media.ToDomain(), nil
}

// AttachVariantMedia links an existing product media row to a variant, assigning
// the next position within the variant's media subset.
func (r *productRepo) AttachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) (domain.VariantMedia, error) {

	defer metrics.ObserveDB("product", "attach_variant_media")(time.Now())

	rows, err := r.db.GetDb().Query(ctx, `
		INSERT INTO variant_media (variant_id, media_id, position)
		VALUES ($1, $2, (SELECT COALESCE(MAX(position), -1) + 1 FROM variant_media WHERE variant_id = $1))
		RETURNING variant_id, media_id, position`,
		pgUUID(variantID), pgUUID(mediaID))
	if err != nil {
		return domain.VariantMedia{}, err
	}

	link, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[model.VariantMedia])
	if err != nil {
		return domain.VariantMedia{}, err
	}

	return link.ToDomain(), nil
}

// AttachNewOrExistingVariantMedia appends media to a variant in a single
// transaction: it inserts any brand-new product_media rows, then inserts the
// variant_media links. Existing variant_media links are never removed.
func (r *productRepo) AttachNewOrExistingVariantMedia(ctx context.Context, newMedia []domain.ProductMedia, links []domain.VariantMedia) error {

	defer metrics.ObserveDB("product", "attach_new_or_existing_variant_media")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, m := range newMedia {
		_, err = tx.Exec(ctx, `
			INSERT INTO product_media (id, product_id, type, url, alt_text, position)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			pgUUID(m.ID), pgUUID(m.ProductID), m.Type, m.URL, m.AltText, m.Position,
		)
		if err != nil {
			return err
		}
	}

	for _, vm := range links {
		_, err = tx.Exec(ctx, `
			INSERT INTO variant_media (variant_id, media_id, position)
			VALUES ($1, $2, $3)`,
			pgUUID(vm.VariantID), pgUUID(vm.MediaID), vm.Position,
		)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

// DetachVariantMedia removes a variant_media link only; the underlying media
// row and file are untouched.
func (r *productRepo) DetachVariantMedia(ctx context.Context, variantID, mediaID uuid.UUID) error {

	defer metrics.ObserveDB("product", "detach_variant_media")(time.Now())

	tag, err := r.db.GetDb().Exec(ctx, `
		DELETE FROM variant_media WHERE variant_id = $1 AND media_id = $2`,
		pgUUID(variantID), pgUUID(mediaID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrVariantMediaNotFound
	}

	return nil
}

// ReorderVariantMedia bulk-updates positions within a single variant's media
// subset in one transaction.
func (r *productRepo) ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []domain.PositionUpdate) error {

	defer metrics.ObserveDB("product", "reorder_variant_media")(time.Now())

	tx, err := r.db.GetDb().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range positions {
		tag, err := tx.Exec(ctx, `
			UPDATE variant_media SET position = $1
			WHERE variant_id = $2 AND media_id = $3`,
			p.Position, pgUUID(variantID), pgUUID(p.ID),
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrVariantMediaNotFound
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}
