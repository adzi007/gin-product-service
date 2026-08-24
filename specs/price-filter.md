# Task: Add Price Range Filter to Product List Endpoint

## Objective

Modify the product list endpoint to support filtering products by a user-provided price range.
The existing endpoint returns a paginated list of products. Add optional `minPrice` and `maxPrice` query parameters so clients can filter products based on their price.
The existing behavior must remain unchanged when neither parameter is provided.

## Existing Endpoint

```http
GET /api/v1/products
```

### Existing Query Parameters

```
page        - optional pagination page
per_page    - optional number of items per page
search      - optional product search keyword
category_id - optional category filter
status      - optional category filter
sort_by     - optional category filter
sort_dir    - optional category filter
```

Example:

```
GET /api/v1/products?page=1&status=draft&category_id=7&search=Smart
```

## New Query Parameters

Add the following optional parameters:

```
minPrice    - optional minimum price filter
maxPrice    - optional maximum price filter
```

Example:

```http
GET /api/v1/products?minPrice=100000&maxPrice=500000
```

This should return products whose price is between `100000` and `500000`, inclusive.

## Filtering Rules

The filtering behavior must be:

### 1. Neither parameter provided

Return products exactly as before.

```http
GET /api/v1/products
```

No price filtering should be applied.

### 2. Only `minPrice` provided

Return products where:

```
price >= minPrice
```

Example:

```http
GET /api/v1/products?minPrice=100000
```

Expected condition:

```sql
price >= 100000
```

### 3. Only `maxPrice` provided

Return products where:

```text
price <= maxPrice
```

Example:

```http
GET /api/v1/products?maxPrice=500000
```

Expected condition:

```sql
price <= 500000
```

### 4. Both parameters provided

Return products where:

```text
minPrice <= price <= maxPrice
```

Example:

```http
GET /api/v1/products?minPrice=100000&maxPrice=500000
```

Expected condition:

```sql
price >= 100000
AND price <= 500000
```

Both boundaries are inclusive.

## Validation

Validate the query parameters before executing the database query.

### Invalid values

Reject requests when:

- `minPrice` is not a valid number
- `maxPrice` is not a valid number
- `minPrice` is negative
- `maxPrice` is negative
- `minPrice` is greater than `maxPrice`

Example:

```http
GET /api/v1/products?minPrice=abc
```

should return a `400 Bad Request`.

Example:

```http
GET /api/v1/products?minPrice=500000&maxPrice=100000
```

should return a `400 Bad Request`.

## API Response

The existing response structure must not change.

## Implementation Requirements

Follow the existing project architecture and conventions.
Before modifying code:

1. Locate the existing product list endpoint.
2. Identify how query parameters are currently parsed and validated.
3. Identify the service/use-case responsible for retrieving products.
4. Identify the repository/database query used to retrieve products.
5. Follow the existing patterns for passing filters through these layers.
6. Reuse existing validation, error handling, pagination, and response patterns where possible.

Do not introduce a new architectural pattern or dependency unless the existing codebase requires it.
The price filter should be applied at the repository layer rather than retrieving all products and filtering them in Go.
Existing filters such as search and category must continue to work together with the new price filters.

For example:

```http
GET /api/v1/products?search=shirt&category=clothing&minPrice=100&maxPrice=500
```

should apply all filters together.

## Database Query Behavior

The final database query should only include price conditions when the corresponding parameters are provided.

Conceptually:

```
No price parameters:
    existing query

minPrice only:
    existing query + max_price >= minPrice

maxPrice only:
    existing query + start_price <= maxPrice

minPrice + maxPrice:
    existing query + max_price >= minPrice + start_price <= maxPrice
```

Avoid constructing SQL using raw user input.

Use the project's existing parameterized query / ORM mechanisms.

## Pagination

Price filtering must happen before pagination is applied.

The pagination `total` must represent the number of products matching **all active filters**, including the price range.

For example, if 100 products exist but only 15 match the requested price range:

```
total = 15
```

not:

```
total = 100
```

## Tests

Add or update tests following the project's existing testing conventions.

At minimum, cover:

### Query parameter parsing

* No price parameters
* `minPrice` only
* `maxPrice` only
* Both parameters

### Validation

* Invalid `minPrice`
* Invalid `maxPrice`
* Negative `minPrice`
* Negative `maxPrice`
* `minPrice > maxPrice`

### Filtering

- Products matching the minimum price
- Products matching the maximum price
- Products inside the range
- Products outside the range
- Exact boundary values

### Combined filters

Verify that price filtering works together with existing:

- Search
- Category
- Pagination

### Regression

Existing product-list behavior without price parameters must continue to work.

## Acceptance Criteria

The task is complete when:

- [ ] `minPrice` is supported by `GET /api/v1/products`.
- [ ] `maxPrice` is supported by `GET /api/v1/products`.
- [ ] Both parameters are optional.
- [ ] Minimum and maximum prices are inclusive.
- [ ] Invalid price values return `400 Bad Request`.
- [ ] Negative prices are rejected.
- [ ] `minPrice > maxPrice` is rejected.
- [ ] Existing filters continue to work.
- [ ] Price filtering is performed at the database/repository level.
- [ ] Pagination is applied after filtering.
- [ ] Pagination totals reflect the filtered result.
- [ ] Existing API response structure is unchanged.
- [ ] Existing behavior without price parameters is preserved.
- [ ] Appropriate unit/integration tests are added or updated.
- [ ] All existing tests continue to pass.
- [ ] No unnecessary dependencies or architectural changes are introduced.