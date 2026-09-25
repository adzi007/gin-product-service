# Validation: Admin Auth0 Authorization

**Feature**: `006-admin-auth0-authorization`
**Validated**: 2026-09-26
**Validator**: implementation agent (automated + local smoke checks)

This file records what was actually executed. Checks that need an Auth0-configured
environment with real staff credentials, or a reachable Postgres, are listed as
**unavailable** with the reason and the substitute coverage.

## 1. Automated gates

| Command | Result |
| --- | --- |
| `gofmt -l .` | No output (all files formatted) |
| `go vet ./...` | Exit 0, no diagnostics |
| `go test ./... -count=1` | All packages `ok`; no failures |
| `go test -race ./internal/infrastructure/auth0/... ./internal/delivery/http/... -count=1` | All packages `ok`; no data races reported |
| `go test ./internal/infrastructure/auth0/... ./internal/delivery/http/... ./cmd/server/... -count=1` | All packages `ok` |
| `git diff --check` | Exit 0, no whitespace errors |

Note: `gofmt -w` also realigned struct field tags in
`internal/delivery/http/dto/review.go`, which was not gofmt-clean at the start of
this work. The change is whitespace-only and unrelated to authorization behavior.

### What the automated tests cover

- **Verifier** (`internal/infrastructure/auth0`): RS256 signature and exact `kid`
  selection, rejected HS256/`none`/forged credentials, exact issuer, string and
  array audience, required `exp`, future `nbf`/`iat`, zero expiry leeway, opaque
  subject, missing/empty/duplicate/malformed `permissions`.
- **JWKS cache** (`internal/infrastructure/auth0`): exact-`kid` lookup across
  multiple keys, rejected invalid/duplicate/incompatible keys, five-minute cache
  hit with no network I/O, refetch after expiry, one bounded unknown-`kid` refresh
  per 60 seconds, coalesced concurrent fetches (8 goroutines → 1 fetch), oversized
  response rejection, fetch timeout, and fail-closed behavior on outage including
  no revival of an expired set.
- **Admin middleware** (`internal/delivery/http/middleware/admin`): namespaced
  identity context, exact permission membership, `RequireAuth` before
  `RequirePermission`, fail-closed nil verifier, malformed/duplicate/ambiguous
  bearer headers, fixed 401/403 envelope, bounded outcome metric, and denial logs
  that carry outcome/route but not the token, subject, or permission list.
- **Route policy** (`internal/delivery/http`): all 34 admin routes accept a token
  with their exact permission and reject a token without it; the 15-credential
  denial matrix (missing, malformed, forged, unknown `kid`, expired, wrong issuer,
  wrong audience, HS256 confusion, `alg=none`, unrelated/similar/absent/empty
  permissions) returns the documented status, code, and envelope with no handler
  invocation; the four customer routes keep HS256 identity; the five public GET
  routes reach their handlers under four header states; the registered route set is
  asserted to be exactly 43 with an explicit policy, so an unclassified new
  business route fails the test.
- **Composition** (`internal/wire`, `cmd/server`): the container owns one non-nil
  admin verifier built from validated configuration; missing/malformed
  `AUTH0_DOMAIN`/`AUTH0_AUDIENCE` prevents serving; a valid configuration wires one
  verifier with no network fetch during construction.

## 2. Generated API documentation

| Command | Result |
| --- | --- |
| `swag init -g cmd/main.go -o docs --parseInternal` | Regenerated `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` (+1738/−3 lines) |
| `git diff --check` | Exit 0 |

Verified in the regenerated `docs/swagger.json`:

- `securityDefinitions` contains `BearerAuth` (`type: apiKey`, `name: Authorization`, `in: header`).
- 43 operations are documented; exactly 34 carry `security: [BearerAuth]` and all 34 declare both `401` and `403` responses.

## 3. Local service smoke checks

The service was started locally without a real Auth0 tenant so authorization
behavior could be exercised end to end:

```
AUTH0_DOMAIN=tenant.example.auth0.com AUTH0_AUDIENCE=https://api.example.com/ \
APP_ENV=development go run cmd/main.go
```

The startup route dump confirmed the per-route middleware chains by handler count:
public GET routes 5 handlers, customer review routes 6, admin management routes 7
(tracing + recovery + engine middleware, then `RequireAuth` + `RequirePermission`
before the business handler).

| Request | Observed |
| --- | --- |
| `GET /healthz` | `200` |
| `POST /api/v1/categories` with no credential | `401` `{"error":{"code":"UNAUTHENTICATED","message":"invalid or missing access token","details":{}}}` |
| `GET /api/v1/products/{id}/reviews` with no credential | `401` `UNAUTHENTICATED`, same envelope |
| `GET /api/v1/products/{id}/reviews` with `Authorization: Bearer invalid` | `401` `UNAUTHENTICATED`, same envelope |
| `GET /api/v1/users/me/reviews` with no credential | `401` `{"error":{"code":"UNAUTHENTICATED","message":"missing authorization header","details":{}}}` (existing customer middleware unchanged) |
| `GET /api/v1/secrets` | `404` |
| `PUT /api/v1/products/{id}` (unregistered method) | `404` |

Operational visibility after those denials:

```
http_admin_authorization_outcomes_total{method="GET",outcome="unauthenticated",route="/api/v1/products/:id/reviews"} 2
http_admin_authorization_outcomes_total{method="POST",outcome="unauthenticated",route="/api/v1/categories"} 1
```

The outcome metric uses only the method, the registered route template, and a
bounded outcome. No token, subject, concrete resource id, or permission list
appears as a label.

## 4. Unavailable checks

| Check | Status | Reason and substitute coverage |
| --- | --- | --- |
| Live Auth0 JWKS retrieval and a real Auth0-issued access token on an admin route | **Unavailable** | No Auth0 tenant, API audience, or staff credentials were available in this environment. Substitute: the verifier and route tests run against a local TLS JWKS endpoint with generated RSA keys, covering signature, `kid`, issuer, audience, expiry, and rotation semantics. |
| Public catalog reads against real data (`GET /categories`, `/categories/{id}`, `/categories/dropdown`, `/products`, `/products/{id}` with no token / customer token / Auth0 token / invalid bearer) | **Unavailable** | The configured external Postgres (Neon) was unreachable from this environment, so the DB-backed handlers could not complete. Substitute: `internal/delivery/http/public_catalog_auth_test.go` exercises all five routes under four header states against stub use cases and asserts the handler is reached with no admin denial. |
| `SC-005` staff acceptance exercise (authorized staff actions without extra sign-in prompts; recorded denial envelopes) | **Unverified** | Requires real staff accounts and an Auth0-configured deployment. Automated tests cover the denial envelopes and authorized reachability but cannot measure the sign-in-prompt percentage. |
| Reservation / product-write end-to-end outcome against real PostgreSQL and Redis | **Unavailable** | Same external dependency limitation. Substitute: `admin_authorized_routes_test.go` asserts product update and reservation creation reach their use cases after authorization (`201` for the reservation), and existing business tests remain green. |

## 5. Success criteria status

| Criterion | Status | Evidence |
| --- | --- | --- |
| `SC-001`: every protected route allows a valid token with the assigned permission and rejects a valid token without it with 403 | Verified locally | `TestAdminRoutesAcceptTheirExactPermission`, `TestAdminRoutesRejectUnauthenticatedAndUnauthorizedRequests` |
| `SC-002`: every protected route rejects missing/malformed/invalid/expired tokens with 401 and makes no business change | Verified locally | Denial matrix plus the `routeProbe` assertion that no use case ran on a denied request |
| `SC-003`: all 43 business routes have an explicit policy (34 admin, 5 public, 4 customer) | Verified locally | `TestRouterRegistersEveryBusinessRouteWithAnExplicitPolicy` |
| `SC-004`: customer review journeys keep their pre-feature behavior and no Auth0 token completes one | Verified locally | `TestCustomerReviewRoutesKeepCustomerAuthentication`, `TestRequireAuth_RejectsAuth0RS256Token` |
| `SC-005`: ≥95% of authorized staff actions complete without an additional sign-in prompt | **Unverified** | See section 4; requires a live Auth0 deployment with staff credentials |
