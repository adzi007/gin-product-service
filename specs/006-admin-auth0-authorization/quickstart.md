
# Quickstart Validation: Admin Auth0 Authorization

This is a validation guide for the implementation that follows this plan. See [the route contract](contracts/admin-authorization.md) for all 43 method/path policies and [the runtime model](data-model.md) for token and cache rules.

## Prerequisites

1. Configure an Auth0 API that issues RS256 access tokens for this service's API identifier. Enable **Add Permissions in the Access Token** and assign the exact permissions needed for test staff accounts. Use the same Auth0 domain as the token `iss`.
2. Configure the service's existing PostgreSQL and Redis dependencies as described in `README.md` and `.env.example`. Set `AUTH0_DOMAIN` to the Auth0 host name (no `https://` or trailing path), `AUTH0_AUDIENCE` to the exact API identifier, and keep the existing `API_JWT_SECRET` for customer tokens. Do not commit real tokens or secrets.
3. Obtain four test credentials: a valid Auth0 token with `products:reviews:read`, a valid Auth0 token without that grant, a valid customer HS256 token for the review author, and a deliberately invalid token. Use a seeded product/review ID for business-success checks. The negative tokens may have no business side effects.
4. Start the service using `go run cmd/main.go`. A missing or malformed Auth0 configuration must prevent serving, rather than exposing management routes.

## Automated validation

After implementation, run:

```sh
go test ./internal/infrastructure/auth0/... ./internal/delivery/http/... ./cmd/server/... -count=1
go test ./... -count=1
go test -race ./internal/infrastructure/auth0/... ./internal/delivery/http/... -count=1
```

The verifier and middleware tests should use generated RSA keys and a local JWKS test server. They must cover signature and `kid` selection, cache hit and rotation, failed/limited refresh, wrong `alg`/`iss`/`aud`, expiry and future `nbf`/`iat`, malformed permissions, exact grant matching, duplicate/malformed Authorization headers, fixed error shape, and no handler invocation on denial. The route test should compare all 43 registered business methods/paths to the contract, including the admin/customer/public split.

For generated API docs, run the repository's documented generator after updating annotations, then verify the generated `docs/` changes:

```sh
swag init -g cmd/main.go -o docs --parseInternal
git diff --check
```

## HTTP smoke checks

Set local shell variables for a running service and acquired test tokens:

```sh
API_BASE=http://localhost:5000/api/v1
PRODUCT_ID='replace-with-seeded-product-id'
REVIEW_ID='replace-with-seeded-review-id'
ADMIN_READ_TOKEN='replace-with-valid-auth0-read-token'
ADMIN_NO_READ_TOKEN='replace-with-valid-auth0-token-without-read-grant'
CUSTOMER_TOKEN='replace-with-valid-customer-token'
```

1. **Public catalog**: `curl -i "$API_BASE/products"` and `curl -i -H 'Authorization: Bearer invalid' "$API_BASE/categories/dropdown"` retain their ordinary catalog outcomes. Repeat for all five public GET routes. An invalid bearer token does not turn these into 401/403.
2. **Admin missing/invalid token**: `curl -i "$API_BASE/products/$PRODUCT_ID/reviews"` and the same request with `Authorization: Bearer invalid` return 401 with `error.code=UNAUTHENTICATED`, a safe `error.message`, and object `error.details`. The business handler is not called.
3. **Admin missing permission**: `curl -i -H "Authorization: Bearer $ADMIN_NO_READ_TOKEN" "$API_BASE/products/$PRODUCT_ID/reviews"` returns 403 with `error.code=FORBIDDEN` and the same envelope shape.
4. **Admin exact permission**: `curl -i -H "Authorization: Bearer $ADMIN_READ_TOKEN" "$API_BASE/products/$PRODUCT_ID/reviews"` reaches the existing read handler and returns its normal result. A different permission alone does not authorize it.
5. **Customer isolation**: With a valid customer token, the customer's create/update/delete/list-own-reviews cases retain their existing outcomes and ownership rules. An Auth0 token used on `GET /users/me/reviews` returns 401; the customer token used on the protected admin review GET returns 401.
6. **Management coverage**: Run route-level checks for every protected method/path in the contract using a valid token with its named grant, then a valid token lacking that exact grant, then no token. Verify the handler is reached only in the first case, and that denied writes leave state unchanged. Business validation errors after authorization are acceptable only when they are the existing handler's normal outcome.
7. **Operational visibility**: Inspect metrics and structured logs after 401 and 403 requests. They identify outcome and route template without token, raw subject, permission list, or concrete resource ID. During a JWKS outage, requests needing an uncached/expired key fail closed.

The staff sign-in prompt outcome in SC-005 requires an acceptance exercise with real staff and Auth0-issued credentials; unit tests cannot measure that percentage. Record that result separately before release.
