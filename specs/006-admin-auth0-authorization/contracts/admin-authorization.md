
# HTTP Contract: Admin Authorization

Applies to the current `/api/v1` business routes. Existing successful request and response payloads, handler behavior, and persistence contracts remain as documented elsewhere, including `specs/001-checkout-inventory-reservation/contracts/create-reservation.openapi.yaml`.

## Credential and response rules

Admin routes require exactly one `Authorization: Bearer <Auth0 RS256 access token>` header. Authentication verifies the signature with the matching `kid` from `https://{AUTH0_DOMAIN}/.well-known/jwks.json` and checks `iss`, `aud`, and validity times. Authorization then requires the exact route permission in the decoded `permissions` string array. `scope` and role names do not grant access here.

| Situation on an admin route | Status | Response body |
| --- | --- | --- |
| Missing, malformed, duplicate, or invalid bearer credential; unsupported algorithm; wrong issuer/audience; expired/not-yet-valid token; unavailable required key | 401 | `{"error":{"code":"UNAUTHENTICATED","message":"invalid or missing access token","details":{}}}` |
| Valid Auth0 token without the exact assigned permission, including absent/empty `permissions` | 403 | `{"error":{"code":"FORBIDDEN","message":"insufficient permission","details":{}}}` |
| Valid Auth0 token with the exact assigned permission | Existing handler status/body | Existing handler contract |

The messages above are safe fixed examples; stable status, code, and object shape are required. `details` is an object, even when empty. Token text, claims, keys, and parser internals do not appear in responses or logs. Authentication and authorization middleware abort before the business handler on denial.

Public catalog routes do not parse or validate a supplied Authorization header, including an invalid one. Customer routes keep `middleware.RequireAuth(jwtSecret)` and its HS256/UUID customer contract; an Auth0 permission cannot substitute for it. Unknown route/method combinations inherit no permission from another route.

## Route access matrix

Each row is one registered method and Gin route template. `admin:<permission>` means the router chain is `admin.RequireAuth(verifier), admin.RequirePermission("<permission>"), handler` in that order.

| Method | Route template | Policy |
| --- | --- | --- |
| POST | `/api/v1/categories` | admin:`categories:create` |
| GET | `/api/v1/categories` | public |
| GET | `/api/v1/categories/dropdown` | public |
| GET | `/api/v1/categories/:id` | public |
| PUT | `/api/v1/categories/:id` | admin:`categories:update` |
| DELETE | `/api/v1/categories/:id` | admin:`categories:delete` |
| POST | `/api/v1/products` | admin:`products:create` |
| GET | `/api/v1/products` | public |
| GET | `/api/v1/products/:id` | public |
| PATCH | `/api/v1/products/:id` | admin:`products:update` |
| DELETE | `/api/v1/products/:id` | admin:`products:delete` |
| POST | `/api/v1/products/:id/restore` | admin:`products:restore` |
| DELETE | `/api/v1/products/:id/purge` | admin:`products:purge` |
| POST | `/api/v1/products/:id/options` | admin:`products:options:create` |
| PATCH | `/api/v1/products/:id/options/reorder` | admin:`products:options:update` |
| PATCH | `/api/v1/products/:id/options/:option_id` | admin:`products:options:update` |
| DELETE | `/api/v1/products/:id/options/:option_id` | admin:`products:options:delete` |
| POST | `/api/v1/products/:id/options/:option_id/values` | admin:`products:options:create` |
| PATCH | `/api/v1/products/:id/options/:option_id/values/:value_id` | admin:`products:options:update` |
| DELETE | `/api/v1/products/:id/options/:option_id/values/:value_id` | admin:`products:options:delete` |
| POST | `/api/v1/products/:id/variants` | admin:`products:variants:create` |
| POST | `/api/v1/products/:id/variants/bulk` | admin:`products:variants:create` |
| PATCH | `/api/v1/products/:id/variants/bulk` | admin:`products:variants:update` |
| POST | `/api/v1/products/:id/variants/bulk-delete` | admin:`products:variants:delete` |
| PATCH | `/api/v1/products/:id/variants/reorder` | admin:`products:variants:update` |
| POST | `/api/v1/products/:id/media` | admin:`products:media:create` |
| PATCH | `/api/v1/products/:id/media/reorder` | admin:`products:media:update` |
| PATCH | `/api/v1/products/:id/media/:media_id` | admin:`products:media:update` |
| DELETE | `/api/v1/products/:id/media/:media_id` | admin:`products:media:delete` |
| GET | `/api/v1/products/:id/reviews` | admin:`products:reviews:read` |
| GET | `/api/v1/products/:id/reviews/summary` | admin:`products:reviews:read` |
| POST | `/api/v1/products/:id/reviews` | customer HS256 |
| GET | `/api/v1/reviews/:reviewId` | admin:`reviews:read` |
| PATCH | `/api/v1/reviews/:reviewId` | customer HS256 |
| DELETE | `/api/v1/reviews/:reviewId` | customer HS256 |
| GET | `/api/v1/users/me/reviews` | customer HS256 |
| PATCH | `/api/v1/variants/:id` | admin:`variants:update` |
| DELETE | `/api/v1/variants/:id` | admin:`variants:delete` |
| POST | `/api/v1/variants/:id/restore` | admin:`variants:restore` |
| POST | `/api/v1/variants/:id/media` | admin:`variants:media:create` |
| PATCH | `/api/v1/variants/:id/media/reorder` | admin:`variants:media:update` |
| DELETE | `/api/v1/variants/:id/media/:media_id` | admin:`variants:media:delete` |
| POST | `/api/v1/inventory/reservations` | admin:`inventory:reservations:create` |

There are 43 rows: 34 admin, five public, and four customer. `categories:read` and `products:read` are not required by the public GET routes. `/healthz`, `/readyz`, `/metrics`, Swagger, and unmatched routes are outside this business-route matrix.

## Deployment contract

- `AUTH0_DOMAIN`: required Auth0 host name, without a scheme or path; used to derive HTTPS issuer and JWKS URL.
- `AUTH0_AUDIENCE`: required exact API identifier expected in the access-token `aud`.
- `API_JWT_SECRET`: existing customer HS256 secret; kept separate from Auth0 configuration.

Auth0 must issue RS256 access tokens for that audience and enable **Add Permissions in the Access Token** for the API. A custom Auth0 domain must match the token issuer.
