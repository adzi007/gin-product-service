# Product Reviews & Ratings API Specification

Base path: `/api/v1`

Auth: All write endpoints require an authenticated user (`Authorization: Bearer <token>`), resolved to `user_id`. Read endpoints are public unless noted.

---

## 1. Create a Review

Creates a review for a product (optionally tied to a purchased variant). Enforces the `uq_user_product_review` constraint — one review per user per product.

**`POST /products/{productId}/reviews`**

### Path Params
| Name | Type | Description |
|---|---|---|
| productId | uuid | Product being reviewed |

### Request Body
```json
{
  "variant_id": "8b1e2c34-...-uuid",   // optional
  "rating": 5,                          // required, int 1-5
  "title": "Exactly what I needed",     // optional, max ~150 chars
  "comment": "Great build quality, fast shipping." // optional, max ~5000 chars
}
```

### Validation Rules
- `rating`: required, integer, 1–5 inclusive.
- `title`: optional, trimmed, max length enforced (e.g. 150 chars).
- `comment`: optional, max length enforced (e.g. 5000 chars).
- `variant_id`: if provided, must belong to `productId`.


### Response `201 Created`
```json
{
  "id": "f47ac10b-...-uuid",
  "product_id": "3fa85f64-...-uuid",
  "variant_id": "8b1e2c34-...-uuid",
  "user_id": "6c9e2c1a-...-uuid",
  "rating": 5,
  "title": "Exactly what I needed",
  "comment": "Great build quality, fast shipping.",
  "created_at": "2026-08-29T10:15:00Z",
  "updated_at": "2026-08-29T10:15:00Z"
}
```

### Error Responses
| Status | Case |
|---|---|
| 400 | Invalid rating / body validation failure |
| 401 | Not authenticated |
| 404 | Product or variant not found |
| 409 | User already reviewed this product (`uq_user_product_review`) |

---

## 2. List Reviews for a Product

Public, paginated, filterable/sortable listing — the main "Reviews" tab on a PDP.

**`GET /products/{productId}/reviews`**

### Query Params
| Name | Type | Default | Description |
|---|---|---|---|
| page | int | 1 | Page number |
| limit | int | 20 | Page size (max 100) |
| rating | int (1-5) | — | Filter to a specific star rating |
| verified_only | bool | false | Only verified-purchase reviews |
| has_comment | bool | false | Only reviews with non-empty `comment` |
| sort | enum | `newest` | `newest`, `oldest`, `highest_rating`, `lowest_rating` |

Uses `idx_reviews_product_date` for the default `newest`/`oldest` sorts.

### Response `200 OK`
```json
{
  "data": [
    {
      "id": "f47ac10b-...-uuid",
      "product_id": "3fa85f64-...-uuid",
      "variant_id": "8b1e2c34-...-uuid",
      "user": {
        "id": "6c9e2c1a-...-uuid",
        "display_name": "Jordan K."
      },
      "rating": 5,
      "title": "Exactly what I needed",
      "comment": "Great build quality, fast shipping.",
      "created_at": "2026-08-29T10:15:00Z",
      "updated_at": "2026-08-29T10:15:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total_items": 134,
    "total_pages": 7
  }
}
```

> Note: `user.display_name` is hydrated from the User/Auth Service using `user_id` — this service only stores the ID. Avoid exposing full user records here.

---

## 3. Product Rating Summary

Aggregate stats used for the star-rating widget and histogram near the top of a PDP.

**`GET /products/{productId}/reviews/summary`**

### Response `200 OK`
```json
{
  "product_id": "3fa85f64-...-uuid",
  "average_rating": 4.3,
  "total_reviews": 134,
  "rating_breakdown": {
    "5": 80,
    "4": 30,
    "3": 12,
    "2": 7,
    "1": 5
  }
}
```

---

## 4. Get a Single Review

**`GET /reviews/{reviewId}`**

### Response `200 OK`
Same shape as a single item in the list endpoint (section 2).

### Errors
| Status | Case |
|---|---|
| 404 | Review not found |

---

## 5. Update Own Review

Only the review's author can edit it. Editing rating/comment should reset moderation status if a moderation workflow exists (see notes at bottom).

**`PATCH /reviews/{reviewId}`**

### Request Body (all optional, at least one required)
```json
{
  "rating": 4,
  "title": "Updated after 3 months of use",
  "comment": "Still holding up well, minor wear."
}
```

### Response `200 OK`
Full updated review object (same shape as section 1's response), with `updated_at` refreshed.

### Errors
| Status | Case |
|---|---|
| 400 | Invalid body |
| 401 | Not authenticated |
| 403 | Authenticated user is not the review's author |
| 404 | Review not found |

---

## 6. Delete Own Review

**`DELETE /reviews/{reviewId}`**

### Response `204 No Content`

### Errors
| Status | Case |
|---|---|
| 401 | Not authenticated |
| 403 | Not the review's author (or not an admin, if admins are allowed to moderate-delete) |
| 404 | Review not found |

---

## 7. List Current User's Reviews

Powers an "My Reviews" account page.

**`GET /users/me/reviews`**

### Query Params
| Name | Type | Default |
|---|---|---|
| page | int | 1 |
| limit | int | 20 |

### Response `200 OK`
```json
{
  "data": [
    {
      "id": "f47ac10b-...-uuid",
      "product": {
        "id": "3fa85f64-...-uuid",
        "name": "Wireless Mechanical Keyboard",
        "thumbnail_url": "https://cdn.example.com/p/kb-01.jpg"
      },
      "variant_id": "8b1e2c34-...-uuid",
      "rating": 5,
      "title": "Exactly what I needed",
      "comment": "Great build quality, fast shipping.",
      "created_at": "2026-08-29T10:15:00Z",
      "updated_at": "2026-08-29T10:15:00Z"
    }
  ],
  "pagination": { "page": 1, "limit": 20, "total_items": 12, "total_pages": 1 }
}
```

`product` is hydrated from the Product Domain.
`thumbnail_url` is hydrated from Galery with position no. 1


---

## Common Object Reference

### Review Object (canonical shape)
```json
{
  "id": "uuid",
  "product_id": "uuid",
  "variant_id": "uuid | null",
  "user_id": "uuid",
  "rating": "int (1-5)",
  "title": "string | null",
  "comment": "string | null",
  "created_at": "timestamptz (ISO 8601)",
  "updated_at": "timestamptz (ISO 8601)"
}
```
Public listing/detail endpoints replace `user_id` with a minimal hydrated `user` object (`id`, `display_name`) rather than exposing raw auth-service data.

### Standard Error Shape
```json
{
  "error": {
    "code": "REVIEW_ALREADY_EXISTS",
    "message": "You have already reviewed this product.",
    "details": {}
  }
}
```

---

## Notes / Things Not in the Current Schema but Common in E-commerce

These are frequently expected features that would need schema additions if you want them:
- **Helpful votes** ("was this review helpful?") — needs a `review_votes` table.
- **Reported/flagged reviews & moderation status** — needs a `status` column (`pending`/`approved`/`rejected`) and a `review_reports` table.
- **Seller/brand responses to reviews** — needs a `review_responses` table.
- **Review images/media** — needs a `review_media` table.

Happy to spec those out too if you want to extend the schema.
