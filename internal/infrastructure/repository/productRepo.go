package repository

import (
	"context"
	"errors"
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
		INSERT INTO products (id, handle, title, description, vendor, category_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		pgUUID(product.ID),
		product.Handle,
		product.Title,
		product.Description,
		product.Vendor,
		product.CategoryID,
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
			pgNumeric(move.Quantity),
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
			pgNumeric(level.AvailableQty),
			pgNumeric(level.ReservedQty),
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
