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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

type productRepo struct {
	db database.Database
}

func NewProductRepo(db database.Database) domain.ProductRepository {
	return &productRepo{db: db}
}

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
			move.Quantity,
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
			level.AvailableQty,
			level.ReservedQty,
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

// FindAll returns a lightweight page of products (base columns plus the nested
// category reference and min/max variant prices, no nested options/variants/
// media) matching the given filter, plus the total count of matching rows
// before pagination.
func (r *productRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.Product, int, error) {

	defer metrics.ObserveDB("product", "find_all")(time.Now())

	// Build the base conditions. sort_by / sort_dir are whitelisted below
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

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	// total count matching filters (before pagination). The category join is
	// required because the search condition can reference category.name.
	countQuery := "SELECT COUNT(*) FROM products LEFT JOIN category ON products.category_id = category.id" + where
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
		LEFT JOIN LATERAL (
			SELECT MIN(v.price) AS min_price, MAX(v.price) AS max_price
			FROM variants v
			WHERE v.product_id = products.id AND v.is_deleted = false
		) price_stats ON true
		LEFT JOIN LATERAL (
			SELECT pm.type, pm.url, pm.alt_text
			FROM product_media pm
			WHERE pm.product_id = products.id AND pm.position = 1
			LIMIT 1
		) thumbnail ON true
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

	listRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[productListRow])
	if err != nil {
		return nil, 0, err
	}

	products := make([]domain.Product, 0, len(listRows))
	for _, row := range listRows {
		p := row.Product
		if row.CategorySlug != nil {
			p.Category.Slug = *row.CategorySlug
		}
		if row.CategoryName != nil {
			p.Category.Name = *row.CategoryName
		}
		p.Prices.StartPrice = row.StartPrice
		p.Prices.MaxPrice = row.MaxPrice
		if row.ThumbnailURL != nil {
			p.Thumbnail = &domain.ProductThumbnail{
				Type:    *row.ThumbnailType,
				URL:     *row.ThumbnailURL,
				AltText: row.ThumbnailAltText,
			}
		}
		products = append(products, p)
	}

	return products, total, nil
}

// FindByID returns a fully hydrated product (options, variants, media) by UUID.
func (r *productRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.Product, error) {

	defer metrics.ObserveDB("product", "find_by_id")(time.Now())

	return r.findProductBy(ctx, "products.id = $1", pgUUID(id))
}

// FindByHandle returns a fully hydrated product (options, variants, media) by handle.
func (r *productRepo) FindByHandle(ctx context.Context, handle string) (domain.Product, error) {

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
		product, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.Product])
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.Product{}, domain.ErrProductNotFound
			}
			return domain.Product{}, err
		}
		return product, nil
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

	product, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.Product])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, domain.ErrProductNotFound
		}
		return domain.Product{}, err
	}

	return product, nil
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

	option, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.ProductOption])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOption{}, domain.ErrOptionNotFound
		}
		return domain.ProductOption{}, err
	}

	return option, nil
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

	option, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.ProductOption])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOption{}, domain.ErrOptionNotFound
		}
		return domain.ProductOption{}, err
	}

	return option, nil
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

	created, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.ProductOptionValue])
	if err != nil {
		return domain.ProductOptionValue{}, err
	}

	return created, nil
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
		got, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.ProductOptionValue])
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ProductOptionValue{}, domain.ErrOptionValueNotFound
			}
			return domain.ProductOptionValue{}, err
		}
		return got, nil
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

	got, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.ProductOptionValue])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOptionValue{}, domain.ErrOptionValueNotFound
		}
		return domain.ProductOptionValue{}, err
	}

	return got, nil
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

	got, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.ProductOptionValue])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductOptionValue{}, domain.ErrOptionValueNotFound
		}
		return domain.ProductOptionValue{}, err
	}

	return got, nil
}

// findProductBy loads a single products row (with its nested category
// reference) plus its nested options, variants (with per-variant stock) and
// media using separate read queries (no transaction needed for reads).
func (r *productRepo) findProductBy(ctx context.Context, predicate string, arg interface{}) (domain.Product, error) {

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
		return domain.Product{}, err
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[productDetailRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, domain.ErrProductNotFound
		}
		return domain.Product{}, err
	}

	product := row.Product
	if row.CategorySlug != nil {
		product.Category.Slug = *row.CategorySlug
	}
	if row.CategoryName != nil {
		product.Category.Name = *row.CategoryName
	}
	if row.CategoryId != nil {
		product.Category.Id = *row.CategoryId
	}

	options, err := r.findProductOptions(ctx, product.ID)
	if err != nil {
		return domain.Product{}, err
	}
	product.Options = options

	variants, err := r.findProductVariants(ctx, product.ID)
	if err != nil {
		return domain.Product{}, err
	}
	product.Variants = variants

	media, err := r.findProductMedia(ctx, product.ID)
	if err != nil {
		return domain.Product{}, err
	}
	product.Media = media

	return product, nil
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

	options, err := pgx.CollectRows(optionRows, pgx.RowToStructByName[domain.ProductOption])
	if err != nil {
		return nil, err
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

	values, err := pgx.CollectRows(valueRows, pgx.RowToStructByName[domain.ProductOptionValue])
	if err != nil {
		return nil, err
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
		ORDER BY v.created_at ASC, v.id ASC`, pgUUID(productID))
	if err != nil {
		return nil, err
	}
	defer variantRows.Close()

	variants, err := pgx.CollectRows(variantRows, pgx.RowToStructByName[domain.Variant])
	if err != nil {
		return nil, err
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

	mediaLinks, err := pgx.CollectRows(mediaRows, pgx.RowToStructByName[domain.VariantMedia])
	if err != nil {
		return nil, err
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

	media, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.ProductMedia])
	if err != nil {
		return nil, err
	}

	return media, nil
}

// productListRow is the row shape scanned by FindAll: base product columns
// (via the embedded domain.Product) plus the joined category slug/name and the
// computed min/max variant prices. Nested domain fields (Category/Prices) are
// populated manually after scanning.
type productListRow struct {
	domain.Product
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
	domain.Product
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
