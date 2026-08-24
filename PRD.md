# Product & Inventory Microservice: PRD & Technical Architecture Specification

## 1. System Overview & Architecture

This specification defines the Product and Inventory Microservice built in Golang. Inspired by Shopify and Odoo architectures, it handles multi-variant product catalog management, media assignment, multi-location inventory tracking, stock movements, and hold reservations.

### Architectural Principles

* **Architecture Style:** Clean Architecture (DDD principles) and SOLID principles.
* **ID Strategy:** UUID v7 generated in the application layer (`[github.com/google/uuid](https://github.com/google/uuid)`) with PostgreSQL `DEFAULT gen_random_uuid()` fallback.
* **Concurrency Control:** Transactions (`BEGIN ... COMMIT`) for stock movements and atomic reservation handling; optimistic locking for inventory updates.
* **Persistence Layer:** PostgreSQL via SQL drivers or query builders (e.g., `pgx/v5`, `sqlc`).

---

## 2. Go Project Directory Layout

```text
├───cmd
│   ├───server
│   │   ├───gin_server.go
│   │   └───server.go
│   └───main.go
├───config
├───docs
├───internal
│   ├───app                           # all use cases, bussiness logic only
│   │   └───category
│   │   	└───queryUseCase.go
│   ├───delivery
│   │   └───http
│   │       ├───handler
│   │       │	└───category_handler.go
│   │       ├───middleware
│   │       └───router.go
│   ├───domain
│   ├───infrastructure
│   │   ├───database
│   │   │	├───database.go
│   │   │	└───postgres.go
│   │   ├───logger
│   │   ├───redis
│   │   ├───repository
│   │   │	└───categoryRepo.go
│   │   └───s3
│   ├───utils
│   └───wire
│   	└───container.go
└───tmp

```

---

## 3. Golang Domain Models & Repository Interfaces

### 3.1 Domain Entities

```go
package entity

import (
	"time"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type StockMoveType string

const (
	StockMoveIn       StockMoveType = "IN"
	StockMoveOut      StockMoveType = "OUT"
	StockMoveTransfer StockMoveType = "TRANSFER"
	StockMoveAdjust   StockMoveType = "ADJUST"
	StockMoveReserve  StockMoveType = "RESERVE"
	StockMoveUnreserve StockMoveType = "UNRESERVE"
)

type Category struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Thumbnail   *string    `json:"thumbnail,omitempty"`
	Description *string    `json:"description,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

type Product struct {
	ID          uuid.UUID       `json:"id"`
	Handle      string          `json:"handle"`
	Title       string          `json:"title"`
	Description *string         `json:"description,omitempty"`
	Vendor      *string         `json:"vendor,omitempty"`
	CategoryID  int             `json:"category_id"`
	Status      string          `json:"status"` // "draft", "active", "archived"
	Options     []ProductOption `json:"options,omitempty"`
	Variants    []Variant       `json:"variants,omitempty"`
	Media       []ProductMedia  `json:"media,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type ProductOption struct {
	ID        uuid.UUID            `json:"id"`
	ProductID uuid.UUID            `json:"product_id"`
	Name      string               `json:"name"`
	Position  int                  `json:"position"`
	Values    []ProductOptionValue `json:"values,omitempty"`
}

type ProductOptionValue struct {
	ID       uuid.UUID `json:"id"`
	OptionID uuid.UUID `json:"option_id"`
	Value    string    `json:"value"`
	Position int       `json:"position"`
}

type Variant struct {
	ID        uuid.UUID       `json:"id"`
	ProductID uuid.UUID       `json:"product_id"`
	SKU       *string         `json:"sku,omitempty"`
	Barcode   *string         `json:"barcode,omitempty"`
	Title     *string         `json:"title,omitempty"`
	Price     decimal.Decimal `json:"price"`
	Weight    decimal.Decimal `json:"weight"`
	Options   []byte          `json:"options"` // Stores raw JSONB: [{"option":"Color","value":"Black"}]
	Position  int             `json:"position"` // Display order within product
	IsDeleted bool            `json:"is_deleted"`
	Media     []ProductMedia  `json:"media,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type ProductMedia struct {
	ID        uuid.UUID `json:"id"`
	ProductID uuid.UUID `json:"product_id"`
	Type      string    `json:"type"` // "image", "video"
	URL       string    `json:"url"`
	AltText   *string   `json:"alt_text,omitempty"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type InventoryItem struct {
	ID             uuid.UUID        `json:"id"`
	VariantID      uuid.UUID        `json:"variant_id"`
	Description    *string          `json:"description,omitempty"`
	TrackInventory bool             `json:"track_inventory"`
	Levels         []InventoryLevel `json:"levels,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

type Location struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Type      *string   `json:"type,omitempty"`
	Address   []byte    `json:"address,omitempty"` // JSONB payload
  IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
}

type InventoryLevel struct {
	ID              uuid.UUID       `json:"id"`
	InventoryItemID uuid.UUID       `json:"inventory_item_id"`
	LocationID      uuid.UUID       `json:"location_id"`
	AvailableQty    int             `json:"available_qty"`
	ReservedQty     int             `json:"reserved_qty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type StockMove struct {
	ID              uuid.UUID       `json:"id"`
	InventoryItemID uuid.UUID       `json:"inventory_item_id"`
	FromLocationID  *uuid.UUID      `json:"from_location_id,omitempty"`
	ToLocationID    *uuid.UUID      `json:"to_location_id,omitempty"`
	MoveType        StockMoveType   `json:"move_type"`
	Quantity        int             `json:"quantity"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty"`
	Reason          *string         `json:"reason,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ReservationStatus string

const (
    ReservationActive   ReservationStatus = "ACTIVE"
    ReservationReleased ReservationStatus = "RELEASED"
    ReservationExpired  ReservationStatus = "EXPIRED"
)

type Reservation struct {
	ID              uuid.UUID       `json:"id"`
	InventoryItemID uuid.UUID       `json:"inventory_item_id"`
	LocationID      uuid.UUID       `json:"location_id"`
	OrderID         *uuid.UUID      `json:"order_id,omitempty"`
	Quantity        int             `json:"quantity"`
	ReservedAt      time.Time       `json:"reserved_at"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
  Status          ReservationStatus
  ReleasedAt      *time.Time
}

```

### 3.2 Repository Interfaces (`internal/domain/repository`)

```go
package repository

import (
	"context"
	"github.com/google/uuid"
	"your-project/internal/domain/entity"
)

type ProductRepository interface {
	Create(ctx context.Context, product *entity.Product) error
	GetByID(ctx context.Context, id uuid.UUID) (*entity.Product, error)
	GetByHandle(ctx context.Context, handle string) (*entity.Product, error)
	GetFullByID(ctx context.Context, id uuid.UUID) (*entity.Product, error) // Includes options, variants, media
	GetFullByHandle(ctx context.Context, handle string) (*entity.Product, error) // Includes options, variants, media
	UpdateHeader(ctx context.Context, product *entity.Product) error // Updates title, description, vendor, handle, category_id, status only
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error // Soft delete / archive
	Delete(ctx context.Context, id uuid.UUID) error // Hard delete (purge)
	
	// Media assignments
	AddMedia(ctx context.Context, media *entity.ProductMedia) error
	UpdateMedia(ctx context.Context, media *entity.ProductMedia) error
	DeleteMedia(ctx context.Context, mediaID uuid.UUID) error
	AssignMediaToVariant(ctx context.Context, variantID, mediaID uuid.UUID, position int) error
	RemoveMediaFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) error
}

type VariantRepository interface {
	Create(ctx context.Context, variant *entity.Variant) error
	CreateBulk(ctx context.Context, variants []*entity.Variant) error // Bulk create with per-item error reporting
	GetByID(ctx context.Context, id uuid.UUID) (*entity.Variant, error)
	GetByProductID(ctx context.Context, productID uuid.UUID) ([]entity.Variant, error)
	Update(ctx context.Context, variant *entity.Variant) error
	UpdateBulk(ctx context.Context, variants []*entity.Variant) error // Bulk update with per-item error reporting
	SoftDelete(ctx context.Context, id uuid.UUID) error // Sets is_deleted = true
	HardDelete(ctx context.Context, id uuid.UUID) error // Cascades and hard-deletes
	BulkDelete(ctx context.Context, ids []uuid.UUID) error // Bulk soft/hard delete with per-item reporting
	Restore(ctx context.Context, id uuid.UUID) error // Sets is_deleted = false
	UpdatePositions(ctx context.Context, productID uuid.UUID, updates []PositionUpdate) error // Reorder
}

type OptionRepository interface {
	Create(ctx context.Context, option *entity.ProductOption) error
	GetByProductID(ctx context.Context, productID uuid.UUID) ([]entity.ProductOption, error)
	Update(ctx context.Context, option *entity.ProductOption) error
	Delete(ctx context.Context, optionID uuid.UUID) error
	UpdatePositions(ctx context.Context, productID uuid.UUID, updates []PositionUpdate) error
	
	// Option values
	CreateValue(ctx context.Context, value *entity.ProductOptionValue) error
	GetValuesByOptionID(ctx context.Context, optionID uuid.UUID) ([]entity.ProductOptionValue, error)
	UpdateValue(ctx context.Context, value *entity.ProductOptionValue) error
	DeleteValue(ctx context.Context, valueID uuid.UUID) error
	UpdateValuePositions(ctx context.Context, optionID uuid.UUID, updates []PositionUpdate) error
}

type MediaRepository interface {
	CreateBulk(ctx context.Context, media []*entity.ProductMedia) error
	GetByProductID(ctx context.Context, productID uuid.UUID) ([]entity.ProductMedia, error)
	Update(ctx context.Context, media *entity.ProductMedia) error
	Delete(ctx context.Context, mediaID uuid.UUID) error
	UpdatePositions(ctx context.Context, productID uuid.UUID, updates []PositionUpdate) error
	
	// Variant media links
	AttachToVariant(ctx context.Context, variantID, mediaID uuid.UUID, position int) error
	DetachFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) error
	GetVariantMedia(ctx context.Context, variantID uuid.UUID) ([]entity.ProductMedia, error)
	UpdateVariantMediaPositions(ctx context.Context, variantID uuid.UUID, updates []PositionUpdate) error
}

type InventoryRepository interface {
	CreateItem(ctx context.Context, item *entity.InventoryItem) error
	GetItemByVariantID(ctx context.Context, variantID uuid.UUID) (*entity.InventoryItem, error)
	GetLevel(ctx context.Context, itemID, locationID uuid.UUID) (*entity.InventoryLevel, error)
	UpsertLevel(ctx context.Context, level *entity.InventoryLevel) error
	
	// Stock movements and transactional state updates
	ExecuteStockMove(ctx context.Context, move *entity.StockMove) error
	CreateReservation(ctx context.Context, res *entity.Reservation) error
	ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error
}

```

---

## 4. RESTful API Specification

### 4.1 Endpoint Matrix

#### Category
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/categories` | Create a product category |
| `GET` | `/v1/categories` | List all categories |

#### Product Header
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/products` | Create product with options, variants, gallery, & initial stock |
| `GET` | `/v1/products` | List products with filtering & pagination |
| `GET` | `/v1/products/:id` | Get product header only |
| `GET` | `/v1/products/:id/full` | Get full product detail (header + options + variants + media) for edit screens |
| `GET` | `/v1/products/:handle` | Get full product detail by product.handle (slug) |
| `PATCH` | `/v1/products/:id` | Update product header fields only (title, description, vendor, handle, category_id, status) |
| `DELETE` | `/v1/products/:id` | Archive product (sets `status = archived`) — soft delete |
| `POST` | `/v1/products/:id/restore` | Un-archive product (restore `status`) |
| `DELETE` | `/v1/products/:id/purge` | Hard delete product (guarded endpoint, cascades if safe) |
| `PUT` | `/v1/products/:id/sync` | Full-graph upsert for import/sync jobs only |

#### Options & Option Values
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/products/:id/options` | Create a new option with initial values |
| `PATCH` | `/v1/products/:id/options/:option_id` | Rename an option |
| `DELETE` | `/v1/products/:id/options/:option_id` | Remove an option and all its values |
| `PATCH` | `/v1/products/:id/options/reorder` | Bulk position update for options |
| `POST` | `/v1/products/:id/options/:option_id/values` | Add a value to an option |
| `PATCH` | `/v1/products/:id/options/:option_id/values/:value_id` | Rename or reorder a value |
| `DELETE` | `/v1/products/:id/options/:option_id/values/:value_id` | Remove a value |

#### Variants
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/products/:id/variants` | Create one variant |
| `POST` | `/v1/products/:id/variants/bulk` | Bulk create variants (import/sync) |
| `PATCH` | `/v1/variants/:id` | Update variant fields (price, sku, barcode, title, weight, etc.) |
| `PATCH` | `/v1/products/:id/variants/bulk` | Bulk update variants, array of `{id, fields}` |
| `DELETE` | `/v1/variants/:id` | Delete variant (soft if has history, hard otherwise) |
| `POST` | `/v1/products/:id/variants/bulk-delete` | Bulk delete variants |
| `POST` | `/v1/variants/:id/restore` | Undo a soft delete |
| `PATCH` | `/v1/products/:id/variants/reorder` | Bulk position update for variants |

#### Media / Gallery
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/products/:id/media` | Upload/attach new media to product gallery (accepts array) |
| `PATCH` | `/v1/products/:id/media/:media_id` | Update media metadata (alt_text, etc.) |
| `DELETE` | `/v1/products/:id/media/:media_id` | Remove media from product entirely (cascades variant_media) |
| `PATCH` | `/v1/products/:id/media/reorder` | Bulk position update for product gallery |
| `POST` | `/v1/variants/:id/media` | Attach existing product media to a variant |
| `DELETE` | `/v1/variants/:id/media/:media_id` | Detach media from variant only (media stays in product) |
| `PATCH` | `/v1/variants/:id/media/reorder` | Bulk position update within a variant's media subset |

#### Inventory & Stock Movements
| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/inventory/stock-moves` | Record stock movement (IN, OUT, TRANSFER, ADJUST, RESERVE, UNRESERVE) |
| `POST` | `/v1/inventory/reservations` | Reserve stock for checkout/cart lock |
| `POST` | `/v1/inventory/reservations/:id/release` | Manually expire or cancel stock reservation |

---

### 4.2 Detailed Endpoint Contracts

#### `POST /v1/products`

Creates a product, its options, option values, and initial variant structure in a single transaction.

**Request Body:**

```json
{
  "handle": "tshirt-basic",
  "title": "Basic Cotton T-Shirt",
  "description": "Soft 100% cotton t-shirt, unisex fit.",
  "vendor": "CottonCo",
  "categoryId": 1,
  "options": [
    {
      "name": "Color",
      "values": ["Black", "White", "Navy"]
    },
    {
      "name": "Size",
      "values": ["S", "M", "L", "XL"]
    }
  ],
  "variants": [
    {
      "title": "Black / S",
      "sku": "TSHIRT-BLK-S",
      "barcode": "1234567890123",
      "price": 19.99,
      "weight": 0.25,
      "options": {
        "Color": "Black",
        "Size": "S"
      },
       "media": [
        {
          "id": "gallery-1",
          "position": 0
        },
        {
          "id": "gallery-2",
          "position": 1
        }
      ],
      "track_inventory": true,
      "stock": 100,
    },
    {
      "title": "White / M",
      "sku": "TSHIRT-WHT-M",
      "barcode": "1234567890456",
      "price": 19.99,
      "weight": 0.26,
      "options": {
        "Color": "White",
        "Size": "M"
      },
      "media": [
        {
          "id": "gallery-3",
          "position": 0
        }
      ],
      "track_inventory": true,
      "stock": 100,
    }
  ],
  "gallery": [
    {
      "id": "gallery-1",
      "type": "image",
      "url": "https://cdn.example.com/tshirt/front.jpg",
      "altText": "Black t-shirt front view",
      "position": 0
    },
    {
      "id": "gallery-2",
      "type": "image",
      "url": "https://cdn.example.com/tshirt/back.jpg",
      "altText": "Black t-shirt back view",
      "position": 1
    },
    {
      "id": "gallery-3",
      "type": "image",
      "url": "https://cdn.example.com/tshirt/white.jpg",
      "altText": "White t-shirt",
      "position": 2
    }
  ],
}

```

**Response (`201 Created`):**

```json
{
  "status": "success",
  "data": {
    "id": "019154a1-8d2b-7c0a-9e12-32b001010001",
    "handle": "ergonomic-cotton-hoodie",
    "title": "Ergonomic Cotton Hoodie",
    "vendor": "Acme Apparel",
    "category_id": 1,
    "options": [
      {
        "id": "019154a1-8d2b-7c0a-9e12-32b001010002",
        "name": "Color",
        "position": 1,
        "values": [
          {"id": "019154a1-8d2b-7c0a-9e12-32b001010003", "value": "Black", "position": 1},
          {"id": "019154a1-8d2b-7c0a-9e12-32b001010004", "value": "Navy", "position": 2}
        ]
      }
    ],
    "created_at": "2026-08-16T16:00:00Z"
  }
}

```

---

#### `GET /v1/products/:id_or_handle`

Returns full product info, options, media gallery, and active variants.

**Response (`200 OK`):**

```json
{
  "status": "success",
  "data": {
    "id": "019154a1-8d2b-7c0a-9e12-32b001010001",
    "handle": "ergonomic-cotton-hoodie",
    "title": "Ergonomic Cotton Hoodie",
    "category_id": 1,
    "media": [
      {
        "id": "019154a1-8d2b-7c0a-9e12-32b001010099",
        "url": "https://cdn.store.com/media/hoodie-main.png",
        "alt_text": "Front view of hoodie",
        "position": 1
      }
    ],
    "variants": [
      {
        "id": "019154a1-8d2b-7c0a-9e12-32b001010010",
        "sku": "HOODIE-BLK-M",
        "price": "59.99",
        "options": [
          {"option": "Color", "value": "Black"},
          {"option": "Size", "value": "M"}
        ],
        "media": [
          {
            "id": "019154a1-8d2b-7c0a-9e12-32b001010099",
            "url": "https://cdn.store.com/media/hoodie-main.png",
            "position": 1
          }
        ]
      }
    ]
  }
}

```

---

#### `POST /v1/inventory/stock-moves`

Executes an inventory move and recalculates `inventory_levels` atomically inside a single database transaction.

**Request Body:**

```json
{
  "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
  "from_location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
  "to_location_id": "",
  "move_type": "IN",
  "quantity": 100,
  "reason": "Initial warehouse stock receipt"
}

```

```json
{
  "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
  "from_location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
  "to_location_id": "019154a1-8d2b-7c0a-9e12-32b001010088",
  "move_type": "TRANSFER",
  "quantity": 100,
  "reason": "Relocation place"
}

```

```json
{
  "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
  "from_location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
  "to_location_id": "",
  "move_type": "ADJUST",
  "quantity": 100,
  "reason": "Admin corection or other reason"
}

```

**Response (`200 OK`):**

```json
{
  "status": "success",
  "data": {
    "move_id": "019154a1-8d2b-7c0a-9e12-32b001010888",
    "inventory_item_id": "019154a1-8d2b-7c0a-9e12-32b001010050",
    "location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
    "available_qty": "100",
    "reserved_qty": "0"
  }
}

```

#### `POST /v1/inventory/reservations`

Executes an reservation `reservations` atomically inside a single database transaction.

**Request Body:**

```json
{
  "inventory_item_id": "...",
  "location_id": "...",
  "quantity": 2,
  "order_id": "...",
  "expires_at": "..."
}

```

**Response (`200 OK`):**

```json
{
  "status": "success",
  "data": {
    "id": "...",
    "inventory_item_id": "...",
    "location_id": "...",
    "quantity": 2,
    "status": "ACTIVE",
    "expires_at": "..."
  }
}

```

---

## 4.3 Product Update & Delete Detailed Specifications

### Product Header Update (`PATCH /v1/products/:id`)

**Request Body:**
```json
{
  "title": "Updated T-Shirt Name",
  "description": "New description",
  "vendor": "NewVendor",
  "handle": "new-handle-slug",
  "category_id": 2,
  "status": "active"
}
```

**Response (`200 OK`):**
```json
{
  "status": "success",
  "data": {
    "id": "019154a1-8d2b-7c0a-9e12-32b001010001",
    "handle": "new-handle-slug",
    "title": "Updated T-Shirt Name",
    "category_id": 2,
    "status": "active",
    "updated_at": "2026-08-21T10:30:00Z"
  }
}
```

**Business Rules:**
- Only updates product header fields; ignore any nested `options`, `variants`, or `media` in the payload (return `400 Bad Request` if client attempts to update them here).
- `handle` must be unique store-wide; return `409 Conflict` if already taken (excluding the current product).
- `category_id` must reference an existing, non-deleted category; return `404 Not Found` if invalid.
- `status` may only be updated via the dedicated `/restore` endpoint (see below).
- All writes occur in a single transaction.

---

### Product Archive/Restore (`DELETE /v1/products/:id` and `POST /v1/products/:id/restore`)

**Archive Request:** `DELETE /v1/products/:id`

**Response (`200 OK`):**
```json
{
  "status": "success",
  "message": "Product archived",
  "data": {
    "id": "019154a1-8d2b-7c0a-9e12-32b001010001",
    "status": "archived",
    "updated_at": "2026-08-21T10:35:00Z"
  }
}
```

**Restore Request:** `POST /v1/products/:id/restore` with optional body:
```json
{
  "status": "draft"
}
```

**Business Rules:**
- `DELETE` is always a **soft delete** (archive); sets `status = archived`.
- Archived products are excluded from public product listings but remain queryable by ID (for admin/analytics).
- Restoring sets `status` back to the specified state (default `draft` if omitted).
- All variants and media remain intact after archive/restore.
- Reservations on archived product variants may not be created (return `409` if attempted), but existing reservations remain valid until expiry.

---

### Product Hard Delete (`DELETE /v1/products/:id/purge`)

**Request:** `DELETE /v1/products/:id/purge` (optional body):
```json
{
  "force": false
}
```

**Response (`200 OK`):**
```json
{
  "status": "success",
  "message": "Product permanently deleted"
}
```

**Error Response (`409 Conflict`):**
```json
{
  "status": "error",
  "code": "PRODUCT_HAS_HISTORY",
  "message": "Cannot purge product: variant has stock_moves history or associated orders",
  "details": {
    "blockers": [
      {"variant_id": "...", "reason": "has stock moves"},
      {"variant_id": "...", "reason": "has associated orders"}
    ]
  }
}
```

**Business Rules:**
- Purge is **only allowed if**:
  1. Product has never appeared in any order line.
  2. All variants have **zero** `stock_moves` and **zero** `reservations` history ever.
  3. No inventory levels have ever been touched.
- If safe to delete, cascade in **one transaction** in this order:
  1. `variant_media` (delete all rows for all variants)
  2. `product_media` (delete all rows)
  3. `inventory_levels` (delete all rows)
  4. `inventory_items` (delete all rows)
  5. `variants` (delete all rows)
  6. `product_option_values` (delete all rows)
  7. `product_options` (delete all rows)
  8. `products` (delete the product)
- If any step fails, rollback the entire transaction.
- If blocked, return `409` with a detailed list of why (which variants have history, etc.).
- Optional `force` flag is reserved for super-admin recovery workflows (e.g., purge a test product and all its history); document this separately if used.

---

## 5. Business Logic & Invariants

### 5.1 Options & Option Values Management

#### Creating Options

When creating a new option via `POST /v1/products/:id/options`:
- Validate that the option `name` is unique within the product (e.g., no two `Color` options).
- Enforce a max option count (recommend max 3, matching Shopify).
- If the product already has variants, **you must provide default values and assign them to every existing variant** in the same transaction. Two acceptable patterns:
  1. **Auto-assign a default**: require the first value in `values[]` to be the default, and auto-assign it to all existing variants.
  2. **Explicit cascade**: force the client to submit updated `variants.options` for each existing variant as part of the same bulk request.

#### Renaming or Reordering Values

When updating a `product_option_value` via `PATCH /v1/products/:id/options/:option_id/values/:value_id`:
- **Renaming** (e.g., `"Red"` → `"Crimson"`) must, in a single transaction, update the denormalized `variants.options` JSONB for every variant currently selecting that value.
- Without this sync, variant labels drift silently from option definitions — this is entirely the application's responsibility (no DB-enforced FK from `variants.options` into `product_option_values`).
- Update all affected variant rows in a single batch `UPDATE` statement, not in a loop.

#### Deleting Values or Options

When deleting a `product_option_value` or entire option:
- Check if any variant currently selects this value.
- If yes, return `409 Conflict` with the list of affected variant IDs, forcing the client to **either**:
  1. Reassign those variants to a different value in the same option, or
  2. Explicitly delete/soft-delete those variants first, then retry.
- Alternatively, support an explicit `?cascade=true` query parameter that soft-deletes all affected variants in the same transaction, with a clear audit trail.
- **Deleting an entire option** (not just a value) removes a dimension from every variant — same rules apply but scoped to all values in that option.

#### Reordering Options or Values

- `PATCH /v1/products/:id/options/reorder` and `PATCH /v1/products/:id/options/:option_id/values/reorder` accept an array of `{id, position}` and batch-update positions in a single transaction.
- Normalize positions to `0, 1, 2…` within the scope; do not allow gaps or collisions.
- This is a lightweight, frequent operation (drag-and-drop) — keep it separate from structural changes that need deeper validation.

---

### 5.2 Variant Creation & Validation

When creating a variant via `POST /v1/products/:id/variants`:
1. **Option validation**: Ensure the `options` payload (e.g., `{"Color": "Black", "Size": "M"}`) exactly matches the product's current option definitions:
   - All required options are present.
   - Values are valid for their respective options.
   - No extra keys in the payload.
2. **Uniqueness checks**:
   - `(product_id, options)` must be unique — reject `409` on a duplicate combination (no two Black/M variants).
   - `sku` must be globally unique (across all products) — reject `409` if taken.
   - `barcode` should be globally unique if provided — reject `409` if taken.
3. **Inventory item auto-creation**: In the same transaction, create the paired `inventory_items` row (1:1 relationship). A variant without an inventory item is an inconsistent state; never allow it transiently.
4. **Price/weight validation**: Ensure non-negative using `decimal.Decimal`.

---

### 5.3 Variant Update & Delete

#### Updating a Variant (`PATCH /v1/variants/:id`)

**Allowed fields:** price, weight, title, sku, barcode, and other non-structural attributes.

**Business rules:**
- **Cannot change `options` via update** — to change option selection, delete and recreate the variant (or provide a separate "re-map variant options" endpoint if your use case requires it).
- If `sku` or `barcode` are updated, re-validate global uniqueness.
- Price/weight must remain non-negative.
- All changes occur in a single transaction.
- If multiple variants are being updated, support `PATCH /v1/products/:id/variants/bulk` with per-item error reporting (return `200 OK` with an array of `{id, success, error}` — do not fail the entire batch on one bad SKU).

#### Deleting a Variant (`DELETE /v1/variants/:id`)

Branch on history:

1. **No history** (inventory_item has zero `stock_moves` and zero `reservations` ever):
   - Allow a hard delete: cascade delete `variant_media` → `inventory_levels` → `inventory_items` → `variants` in a single transaction.

2. **Has history** (stock_moves or reservations exist):
   - Soft delete only: set `is_deleted = true`.
   - Zero out `inventory_levels.available_qty` and `reserved_qty` (optional: log this as a stock adjustment move).
   - Release any open `reservations` for this variant by creating corresponding `UNRESERVE` stock moves (do not raw-delete reservation rows).
   - Leave `stock_moves` intact as the audit trail.

3. **Has unfulfilled orders**: Decide whether to block outright or force-release with a warning — this is a product decision (recommend blocking for safety).

**Bulk delete** (`POST /v1/products/:id/variants/bulk-delete`):
- Accept an array of variant IDs.
- Return per-variant success/failure (e.g., `[{id: "...", success: true}, {id: "...", success: false, error: "has open orders"}]`).
- Do not fail the entire batch on one blocked variant.

**Constraint:** Enforce that a product must always have at least one non-deleted variant. Block deletion of the last variant, or auto-create a placeholder default variant if deleted.

---

### 5.4 Media Management

#### Attaching Media to Product (`POST /v1/products/:id/media`)

**Request:**
```json
{
  "media": [
    {"type": "image", "url": "https://...", "alt_text": "Description", "position": 0},
    {"type": "video", "url": "https://...", "position": 1}
  ]
}
```

**Business rules:**
- Validate `type` against allowed enum (`image`, `video`).
- Run basic checks before insert: file size limits, dimension requirements, format validation (optional, can be deferred to async processing).
- Positions should be auto-assigned or provided explicitly; normalize them after insert.
- Return created rows with their assigned IDs and final positions.

#### Attaching Media to Variant (`POST /v1/variants/:id/media`)

**Request:**
```json
{
  "media_id": "019154a1-8d2b-7c0a-9e12-32b001010099",
  "position": 0
}
```

**Business rules:**
- **Ownership check (critical)**: Validate that `media_id` belongs to the *same* `product_id` as the variant — prevent IDOR/image leakage across unrelated products.
- Upsert or insert the `variant_media` join row in a single statement.
- Normalize positions on insert or via a separate reorder call.

#### Deleting Media from Product (`DELETE /v1/products/:id/media/:media_id`)

**Business rules:**
- Cascades to delete all `variant_media` rows pointing at this media item (matches schema's `[delete: cascade]`).
- If media is attached to multiple variants, warn in the UI before confirming — this is genuinely destructive from the variant perspective, even though it's a single row delete at the product level.
- Hard delete only; no soft-delete needed for media.

#### Deleting Media from Variant (`DELETE /v1/variants/:id/media/:media_id`)

**Business rules:**
- Only deletes the `variant_media` join row — the underlying `product_media` and file are untouched.
- The media item remains in the product gallery.
- Cheap operation; can be done via a simple DELETE on the join table.

#### Reordering Media

- `PATCH /v1/products/:id/media/reorder` and `PATCH /v1/variants/:id/media/reorder` batch-update positions in a single transaction.
- Normalize positions to `0, 1, 2…` within each scope.

---

### 5.5 Full-Graph Upsert (`PUT /v1/products/:id/sync`)

This endpoint is **for controlled import/sync jobs only** (e.g., nightly PIM feed), not for interactive admin UI saves.

**Request:**
```json
{
  "handle": "tshirt-basic",
  "title": "Basic T-Shirt",
  "description": "100% cotton",
  "category_id": 1,
  "options": [
    {"name": "Color", "position": 1, "values": [{"value": "Black", "position": 1}, {"value": "White", "position": 2}]},
    {"name": "Size", "position": 2, "values": [{"value": "S", "position": 1}, {"value": "M", "position": 2}]}
  ],
  "variants": [
    {"id": "...", "sku": "...", "price": 19.99, "options": {"Color": "Black", "Size": "S"}, ...},
    {"sku": "...", "price": 21.99, "options": {"Color": "White", "Size": "M"}, ...}
  ],
  "media": [
    {"id": "...", "url": "...", "alt_text": "...", "position": 0},
    {"url": "...", "position": 1}
  ]
}
```

**Business rules:**
- This is an explicit **replace-everything** operation — the payload is the full desired state.
- In a single transaction:
  1. Upsert the product header.
  2. Upsert/delete options and values (delete any not in the payload, add/update all in it).
  3. Upsert/delete variants (delete any not listed with an `id`, create/update as needed).
  4. Upsert/delete media and variant_media links.
  5. Auto-create missing `inventory_items` for new variants.
- Do not expose this to your regular admin UI's save button — it should use the granular endpoints so a client bug cannot silently wipe variants or options unintentionally.
- Return the full product state after the upsert for verification.

---

### 5.6 Variant Option Resolution & Media Fallback

* Options are represented as JSON arrays in `variants.options`: `[{"option": "Color", "value": "Black"}, ...]`.
* When a user queries a product, the backend joins `variant_media` with `product_media`.
* **Media Fallback Algorithm:**
  1. If `variant_media` contains specific entries for the active variant, return those image URLs sorted by `variant_media.position ASC`.
  2. If no `variant_media` records exist for that variant, return default images directly from `product_media` sorted by `product_media.position ASC`.

---

### 5.7 Stock Movement & Reservation Invariants

1. **MoveType `IN`:** Increases `inventory_levels.available_qty` at `to_location_id`.
2. **MoveType `OUT`:** Decreases `inventory_levels.available_qty` at `from_location_id`. Requires `available_qty >= requested_qty`.
3. **MoveType `TRANSFER`:** Decreases `available_qty` at `from_location_id` and increases `available_qty` at `to_location_id`.
4. **MoveType `RESERVE`:** Shifts quantity from `available_qty` to `reserved_qty` at the specified `location_id`. Fails if `available_qty < requested_qty`.
5. **MoveType `UNRESERVE`:** Shifts quantity from `reserved_qty` back to `available_qty`.
5. **MoveType `ADJUST`:** Increases or decrease `inventory_levels.available_qty` by comparing the user input and the current `inventory_levels.available_qty` 

---

## 6. Cross-Cutting Rules

### 6.1 Transactions & ACID Guarantees

- **Every write endpoint** must run in a single database transaction. Partial success (e.g., variant created, inventory_items row fails) must never occur.
- **Multi-table writes** (e.g., creating a variant and its paired inventory_items, or deleting options and cascading to variants) must use explicit `BEGIN ... COMMIT` (or `SAVEPOINT` for nested ops).
- Use optimistic locking on `products.updated_at` for concurrent edit detection (optional; recommend for shared-admin scenarios).

### 6.2 Ownership & Path Validation

- **Always verify ownership** before acting: check that `{option_id}`, `{value_id}`, `{variant_id}`, `{media_id}` belong to the `{product_id}` (or `{variant_id}`) in the URL path.
- Do not rely on the ID alone — this closes an easy IDOR-style bug class (e.g., attaching a variant's media from Product A to a variant in Product B).
- Return `404 Not Found` if ownership validation fails (treat as "resource not found" rather than revealing the resource exists elsewhere).

### 6.3 Partial Success in Bulk Operations

- **Bulk create/update/delete** must return per-item success/failure, not a single pass/fail for the entire batch.
- Return `200 OK` with an array: `{items: [{id: "...", success: true}, {id: "...", success: false, error: "..."}]}`.
- One bad SKU in a batch of 200 variants should not fail the other 199.
- Exception: **reorder operations** (which are inherently atomic) may fail the entire request if positions would end up inconsistent.

### 6.4 Idempotency & Concurrency

- **Idempotency keys** (optional): Recommend on `POST` create and bulk-create endpoints so retried requests (client timeout, network blip) do not create duplicate variants or media.
  - Accept an `Idempotency-Key` header; store it alongside the created resource; if the same key arrives twice, return the original result.
- **Optimistic concurrency** on updates: If your architecture supports it, use `updated_at` or a `version` integer on `products` and `variants`.
  - Clients include the current version in the `PATCH` request; reject (`409 Conflict`) if it doesn't match current state.
  - This prevents silent overwrites when two admins edit the same product simultaneously.

---

## 7. Error Handling & Validation

### Standard Error Response Format

```json
{
  "status": "error",
  "code": "INSUFFICIENT_STOCK",
  "message": "Requested quantity exceeds available stock level at specified location",
  "details": {
    "location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
    "available_qty": 5.0,
    "requested_qty": 10.0
  }
}
```

### Key Domain Error Definitions

**Product Operations:**
- `ERR_PRODUCT_NOT_FOUND` (404): Product ID does not exist.
- `ERR_INVALID_HANDLE` (400): `handle` is empty or contains invalid characters.
- `ERR_HANDLE_ALREADY_EXISTS` (409): `handle` is already in use by another product.
- `ERR_CATEGORY_NOT_FOUND` (404): Referenced category ID does not exist or is deleted.
- `ERR_CANNOT_UPDATE_NESTED` (400): Client attempted to update options/variants/media via the product header endpoint; these must use their own endpoints.
- `ERR_PRODUCT_HAS_HISTORY` (409): Cannot purge product due to stock_moves or order associations.

**Option Operations:**
- `ERR_OPTION_NOT_FOUND` (404): Option ID does not belong to this product.
- `ERR_OPTION_NAME_DUPLICATE` (409): Option name already exists on this product.
- `ERR_OPTION_VALUE_NOT_FOUND` (404): Option value ID does not exist.
- `ERR_OPTION_VALUE_IN_USE` (409): Cannot delete a value that is currently selected by one or more variants; return list of affected variant IDs.
- `ERR_MAX_OPTIONS_EXCEEDED` (422): Product would exceed the maximum option count (e.g., 3).

**Variant Operations:**
- `ERR_VARIANT_NOT_FOUND` (404): Variant ID does not exist.
- `ERR_INVALID_OPTIONS_PAYLOAD` (400): Variant options do not match product's option schema (missing option, invalid value, extra keys, etc.).
- `ERR_DUPLICATE_VARIANT` (409): A variant with the same `(product_id, options)` combination already exists.
- `ERR_SKU_ALREADY_EXISTS` (409): SKU is globally unique but is already in use.
- `ERR_BARCODE_ALREADY_EXISTS` (409): Barcode is globally unique but is already in use.
- `ERR_CANNOT_DELETE_LAST_VARIANT` (409): Cannot delete the only non-deleted variant in a product.
- `ERR_VARIANT_HAS_ORDERS` (409): Cannot delete a variant with unfulfilled associated orders (optional; per business rules).
- `ERR_INVALID_PRICE_OR_WEIGHT` (400): Price or weight is negative.

**Media Operations:**
- `ERR_MEDIA_NOT_FOUND` (404): Media ID does not exist or doesn't belong to this product.
- `ERR_INVALID_MEDIA_ASSIGNMENT` (400): Attempted to attach media from a different product to this variant (IDOR check).
- `ERR_MEDIA_TYPE_INVALID` (400): Media type is not one of the allowed values (e.g., not `image` or `video`).
- `ERR_VARIANT_MEDIA_NOT_FOUND` (404): Variant-media link does not exist.

**Inventory Operations:**
- `ERR_INSUFFICIENT_STOCK` (422): Requested quantity exceeds `available_qty` at the specified location.
- `ERR_INVALID_STOCK_MOVE_TYPE` (400): Stock move type is not valid (must be one of IN, OUT, TRANSFER, ADJUST, RESERVE, UNRESERVE).
- `ERR_MISSING_LOCATION` (400): `to_location_id` or `from_location_id` required for this move type is missing.
- `ERR_RESERVATION_EXPIRED` (410): Reservation has already expired or been released.
- `ERR_RESERVATION_NOT_FOUND` (404): Reservation ID does not exist.

**Concurrency Errors:**
- `ERR_CONFLICT` (409): Optimistic lock conflict — the resource's `updated_at` or `version` does not match. Client should retry with fresh data.

### Request Validation Checklist

All endpoints must validate:
1. **Path parameters** are valid UUIDs (or integers for category IDs) and belong to the expected parent resource.
2. **Required fields** are present and non-empty.
3. **Data type mismatches** (e.g., price is a string instead of a number).
4. **Business constraint violations** (e.g., duplicate SKU, invalid option value, negative quantity).
5. **Ownership** (e.g., variant belongs to the specified product, media belongs to the specified product).

---

## 8. AI Prompt Rules for Code Generation

When feeding this document to AI models (e.g., Cursor, GitHub Copilot, Claude) to build this service:

1. **Entity Instantiation:** Always generate IDs using Go's `uuid.NewV7()` in application services before calling repository store methods.
2. **Transaction Management:** Any operation modifying both `stock_moves` and `inventory_levels` MUST receive an `exec/tx` context wrapper to guarantee database transaction safety.
3. **Monetary Precision:** Do not use `float64` for `price` or `quantity` fields. Always use `[github.com/shopspring/decimal](https://github.com/shopspring/decimal)`.
4. **Context Propagation:** All database and application methods must accept `ctx context.Context` as their first parameter.

## 9. Database Schemas

```dbml

Table products {
  id uuid [pk, default: `gen_random_uuid()`]
  handle text [not null, unique, note: 'Human-readable slug or handle']
  title text [not null]
  description text
  vendor text
  category_id int [not null]
  status text [not null, default: 'draft', note: 'draft, active, archived']
  created_at timestamptz [default: `now()`]
  updated_at timestamptz [default: `now()`]
}

Table product_options {
  id uuid [pk, default: `gen_random_uuid()`]
  product_id uuid [not null]
  name text [not null, note: 'Option name (e.g. Color, Size)']
  position int [default: 0]
  
  indexes {
    (product_id, position)
    (product_id, name) [unique]
  }
}

Table product_option_values {
  id uuid [pk, default: `gen_random_uuid()`]
  option_id uuid [not null]
  value text [not null]
  position int [default: 0]
  
  indexes {
    (option_id, position)
  }
}

Table variants {
  id uuid [pk, default: `gen_random_uuid()`]
  product_id uuid [not null]
  sku text [unique, note: 'Unique SKU across all variants']
  barcode text
  title text
  price numeric(12,2)
  weight numeric(12,3)
  options jsonb [default: `'[]'::jsonb`, note: 'Stores selected option values']
  position int [default: 0, note: 'Display order within product']
  is_deleted boolean [default: false]
  created_at timestamptz [default: `now()`]
  updated_at timestamptz [default: `now()`]
  
  indexes {
    (product_id, position)
  }
}

Table inventory_items {
  id uuid [pk, default: `gen_random_uuid()`]
  variant_id uuid [not null, unique]
  description text
  track_inventory boolean [default: true]
  created_at timestamptz [default: `now()`]
}

Table locations {
  id uuid [pk, default: `gen_random_uuid()`]
  name text [not null]
  type text [note: 'warehouse, store, etc.']
  address jsonb
  is_default bool
  created_at timestamptz [default: `now()`]
}

Table inventory_levels {
  id uuid [pk, default: `gen_random_uuid()`]
  inventory_item_id uuid [not null]
  location_id uuid [not null]
  available_qty integer
  reserved_qty integer
  updated_at timestamptz [default: `now()`]

  indexes {
    (inventory_item_id, location_id) [unique]
  }
}

Enum stock_move_type {
  IN
  OUT
  TRANSFER
  ADJUST
  RESERVE
  UNRESERVE
}

Table stock_moves {
  id uuid [pk, default: `gen_random_uuid()`]
  inventory_item_id uuid [not null]
  from_location_id uuid
  to_location_id uuid 
  move_type stock_move_type [not null]
  quantity integer
  created_by uuid
  reason text
  created_at timestamptz [default: `now()`]
}

Enum reservation_status {
  ACTIVE
  RELEASED
  EXPIRED
}

Table reservations {
  id uuid [pk, default: `gen_random_uuid()`]
  inventory_item_id uuid [not null]
  location_id uuid [not null]
  order_id uuid
  quantity integer
  reserved_at timestamptz [default: `now()`]
  expires_at timestamptz
  status          reservation_status [not null]
  released_at    timestamptz
}

Table category {
  id int [pk]
  name varchar(100)
  slug varchar(100)
  thumbnail varchar(500)
  description text
  created_at timestamp [default: `now()`]
  updated_at datetime
  deleted_at datetime
}

Table product_media {
  id uuid [pk, default: `gen_random_uuid()`]
  product_id uuid [not null]

  type varchar(20) [not null, default: 'image', note: 'image, video']
  url text [not null]
  alt_text text
  position int [not null, default: 0]

  created_at timestamptz [default: `now()`]
  updated_at timestamptz [default: `now()`]

  Indexes {
    (product_id, position)
  }
}

Table variant_media {
  variant_id uuid [not null]
  media_id uuid [not null]
  position int [not null, default: 0]

  Indexes {
    (variant_id, position)
    (media_id)
  }

  indexes {
    (variant_id, media_id) [unique]
  }
}

//////////////////////////////////////////////////////////////
// Relationships (for visualization)
//////////////////////////////////////////////////////////////

Ref: product_options.product_id > products.id
Ref: product_option_values.option_id > product_options.id
Ref: variants.product_id > products.id
Ref: inventory_items.variant_id > variants.id
Ref: inventory_levels.inventory_item_id > inventory_items.id
Ref: inventory_levels.location_id > locations.id
Ref: stock_moves.inventory_item_id > inventory_items.id
Ref: stock_moves.from_location_id > locations.id
Ref: stock_moves.to_location_id > locations.id
Ref: reservations.inventory_item_id > inventory_items.id
Ref: reservations.location_id > locations.id
Ref: products.category_id > category.id
Ref: "reservations"."id" < "reservations"."location_id"
Ref: "reservations"."id" < "reservations"."inventory_item_id"
Ref: "stock_moves"."id" < "stock_moves"."from_location_id"
Ref: product_media.product_id > products.id
Ref: variant_media.variant_id > variants.id
Ref: variant_media.media_id > product_media.id

```