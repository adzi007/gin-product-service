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
	VariantID      *uuid.UUID       `json:"variant_id,omitempty"`
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
	AvailableQty    decimal.Decimal `json:"available_qty"`
	ReservedQty     decimal.Decimal `json:"reserved_qty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type StockMove struct {
	ID              uuid.UUID       `json:"id"`
	InventoryItemID uuid.UUID       `json:"inventory_item_id"`
	FromLocationID  *uuid.UUID      `json:"from_location_id,omitempty"`
	ToLocationID    *uuid.UUID      `json:"to_location_id,omitempty"`
	MoveType        StockMoveType   `json:"move_type"`
	Quantity        decimal.Decimal `json:"quantity"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty"`
	Reason          *string         `json:"reason,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type Reservation struct {
	ID              uuid.UUID       `json:"id"`
	InventoryItemID uuid.UUID       `json:"inventory_item_id"`
	LocationID      uuid.UUID       `json:"location_id"`
	OrderID         *uuid.UUID      `json:"order_id,omitempty"`
	Quantity        decimal.Decimal `json:"quantity"`
	ReservedAt      time.Time       `json:"reserved_at"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
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
	Update(ctx context.Context, product *entity.Product) error
	Delete(ctx context.Context, id uuid.UUID) error
	
	// Media assignments
	AddMedia(ctx context.Context, media *entity.ProductMedia) error
	DeleteMedia(ctx context.Context, mediaID uuid.UUID) error
	AssignMediaToVariant(ctx context.Context, variantID, mediaID uuid.UUID, position int) error
	RemoveMediaFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) error
}

type VariantRepository interface {
	Create(ctx context.Context, variant *entity.Variant) error
	GetByID(ctx context.Context, id uuid.UUID) (*entity.Variant, error)
	GetByProductID(ctx context.Context, productID uuid.UUID) ([]entity.Variant, error)
	Update(ctx context.Context, variant *entity.Variant) error
	SoftDelete(ctx context.Context, id uuid.UUID) error
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

| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/v1/categories` | Create a product category |
| `GET` | `/v1/categories` | List all categories |
| `POST` | `/v1/products` | Create product with options, variant, gallery, & initial stock |
| `GET` | `/v1/products` | List products with filtering & pagination |
| `GET` | `/v1/products/:id` | Get full product detail (Options, Variants, Gallery) by product.id |
| `GET` | `/v1/products/:slug` | Get full product detail (Options, Variants, Gallery) by product.handle |
| `PUT` | `/v1/products/:id` | Update product master info |
| `DELETE` | `/v1/products/:id` | Cascade delete product |
| `POST` | `/v1/products/:id/media` | Upload/Add media item to product gallery |
| `DELETE` | `/v1/products/:id/media/:media_id` | Delete media asset |
| `POST` | `/v1/variants` | Add individual variant to a product |
| `PUT` | `/v1/variants/:id` | Update variant pricing, SKU, or attributes |
| `POST` | `/v1/variants/:id/media` | Assign an existing `product_media` ID to a variant |
| `POST` | `/v1/inventory/stock-moves` | Record stock movement (IN, OUT, TRANSFER, ADJUST) |
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
  "to_location_id": "019154a1-8d2b-7c0a-9e12-32b001010077",
  "move_type": "IN",
  "quantity": 100.0000,
  "reason": "Initial warehouse stock receipt"
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
    "available_qty": "100.0000",
    "reserved_qty": "0.0000"
  }
}

```

---

## 5. Business Logic & Invariants

### 5.1 Variant Option Resolution Strategy

* Options are represented as JSON arrays in `variants.options`: `[{"option": "Color", "value": "Black"}, ...]`.
* When a user queries a product, the backend joins `variant_media` with `product_media`.
* **Media Fallback Algorithm:**
1. If `variant_media` contains specific entries for the active variant, return those image URLs sorted by `variant_media.position ASC`.
2. If no `variant_media` records exist for that variant, return default images directly from `product_media` sorted by `product_media.position ASC`.



### 5.2 Stock Movement & Reservation Invariants

1. **MoveType `IN`:** Increases `inventory_levels.available_qty` at `to_location_id`.
2. **MoveType `OUT`:** Decreases `inventory_levels.available_qty` at `from_location_id`. Requires `available_qty >= requested_qty`.
3. **MoveType `TRANSFER`:** Decreases `available_qty` at `from_location_id` and increases `available_qty` at `to_location_id`.
4. **MoveType `RESERVE`:** Shifts quantity from `available_qty` to `reserved_qty` at the specified `location_id`. Fails if `available_qty < requested_qty`.
5. **MoveType `UNRESERVE`:** Shifts quantity from `reserved_qty` back to `available_qty`.

---

## 6. Error Handling & Validation

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

* `ERR_PRODUCT_NOT_FOUND` (404)
* `ERR_SKU_ALREADY_EXISTS` (409)
* `ERR_INSUFFICIENT_STOCK` (422)
* `ERR_INVALID_MEDIA_ASSIGNMENT` (400): Occurs when trying to map a `product_media` item belonging to Product A to a Variant belonging to Product B.

---

## 7. AI Prompt Rules for Code Generation

When feeding this document to AI models (e.g., Cursor, GitHub Copilot, Claude) to build this service:

1. **Entity Instantiation:** Always generate IDs using Go's `uuid.NewV7()` in application services before calling repository store methods.
2. **Transaction Management:** Any operation modifying both `stock_moves` and `inventory_levels` MUST receive an `exec/tx` context wrapper to guarantee database transaction safety.
3. **Monetary Precision:** Do not use `float64` for `price` or `quantity` fields. Always use `[github.com/shopspring/decimal](https://github.com/shopspring/decimal)`.
4. **Context Propagation:** All database and application methods must accept `ctx context.Context` as their first parameter.

## 8. Database Schemas

```dbml

Table products {
  id uuid [pk, default: `gen_random_uuid()`]
  handle text [not null, note: 'Human-readable slug or handle']
  title text [not null]
  description text
  vendor text
  category_id int [not null]
  created_at timestamptz [default: `now()`]
  updated_at timestamptz [default: `now()`]
}

Table product_options {
  id uuid [pk, default: `gen_random_uuid()`]
  product_id uuid [not null]
  name text [not null, note: 'Option name (e.g. Color, Size)']
  position int [default: 0]
}

Table product_option_values {
  id uuid [pk, default: `gen_random_uuid()`]
  option_id uuid [not null]
  value text [not null]
  position int [default: 0]
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
  is_deleted boolean [default: false]
  created_at timestamptz [default: `now()`]
  updated_at timestamptz [default: `now()`]
}

Table inventory_items {
  id uuid [pk, default: `gen_random_uuid()`]
  variant_id uuid [unique]
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

Table reservations {
  id uuid [pk, default: `gen_random_uuid()`]
  inventory_item_id uuid [not null]
  location_id uuid [not null]
  order_id uuid
  quantity integer
  reserved_at timestamptz [default: `now()`]
  expires_at timestamptz
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