
# Research: Admin Auth0 Authorization

## 1. Token algorithm and trusted metadata

- **Decision**: Verify Auth0 API access tokens with RS256 only. Configure `AUTH0_DOMAIN` as a host name and `AUTH0_AUDIENCE` as the exact Auth0 API identifier. Derive the expected issuer `https://{AUTH0_DOMAIN}/` and the fixed JWKS URL `https://{AUTH0_DOMAIN}/.well-known/jwks.json`. Do not use a secret, URL, or algorithm supplied by the token.
- **Rationale**: The requested Auth0 API contract is asymmetric signing. Auth0's Go API guide uses RS256, a domain-derived issuer, and its API identifier as audience. A token header is untrusted input.
- **Alternatives considered**: Reusing customer HS256 middleware or accepting both RS256 and HS256 would join unrelated trust paths and enable algorithm confusion; rejected. Supporting deliberately configured Auth0 HS256 is outside this feature.
- **Sources**: [Auth0 Go API quickstart](https://auth0.com/docs/quickstart/backend/golang), [Auth0 JSON Web Key Sets](https://auth0.com/docs/secure/tokens/json-web-tokens/json-web-key-sets).

## 2. JWKS lookup, cache, and rotation

- **Decision**: Use a single process-local, concurrency-safe cache of RSA public keys indexed by `kid`, with a five-minute lifetime. On an unknown `kid`, force one coalesced refresh subject to a 60-second minimum interval; retry selection once. Use a bounded HTTPS client and response-size limit. If no current matching key is available, fail closed with 401. A still-current matching cached key can be used during a transient refresh failure.
- **Rationale**: Auth0 may publish multiple keys during rotation, and its guidance recommends a 5–10 minute cache and controlled refresh for an unknown key. Coalescing prevents one rotation or attacker-supplied `kid` values from causing a request storm. Cache only validated public material from the configured endpoint. A token `jku`, `x5u`, or similar header never changes the fetch target.
- **Alternatives considered**: Fetching JWKS on every request adds latency and an availability dependency; indefinite caching misses rotation; accepting stale expired cache entries weakens validation; rejected.
- **Sources**: [Auth0 JSON Web Key Sets](https://auth0.com/docs/secure/tokens/json-web-tokens/json-web-key-sets), [Locate JSON Web Key Sets](https://auth0.com/docs/secure/tokens/json-web-tokens/locate-json-web-key-sets).

## 3. Signature and claim validation

- **Decision**: Select one key whose `kid` exactly matches the JWT header, require compatible RSA signing material, and verify RS256 signature. Validate exact `iss`, configured `aud` membership for either a string or array claim, required `exp`, and `nbf`/`iat` if present. Enforce expiry at its stated time with zero clock leeway. Treat malformed claims, bad signatures, unsupported methods, unknown keys, or unavailable required keys as 401.
- **Rationale**: Verification must establish authenticity and intended recipient before a permission can be trusted. `exp` and `nbf` define the validity window. The spec explicitly disallows expiry grace.
- **Alternatives considered**: Decode-only inspection, `kid` fallback to any cached key, permissive algorithm lists, or positive clock skew are inconsistent with the specified trust boundary.
- **Sources**: [Validate Access Tokens](https://auth0.com/docs/secure/tokens/access-tokens/validate-access-tokens), [Validate JSON Web Tokens](https://auth0.com/docs/secure/tokens/json-web-tokens/validate-json-web-tokens), [JWT Claims](https://auth0.com/docs/secure/tokens/json-web-tokens/json-web-token-claims).

## 4. Permission claim and HTTP separation

- **Decision**: Authorize from the decoded `permissions` string array using exact membership. An absent or empty array yields an authenticated identity with no grants and then 403; non-array, non-string, or blank entries invalidate the token and yield 401. Ignore `scope` for this policy. Add a separate admin `RequireAuth(verifier)` and `RequirePermission(permission)` pair; do not edit the existing customer `middleware.RequireAuth(secret)`.
- **Rationale**: Auth0's API setting **Add Permissions in the Access Token** supplies the requested claim. The existing customer middleware validates HS256 and interprets `sub` as a UUID; admin `sub` is opaque and cannot be used for customer ownership.
- **Alternatives considered**: Using Auth0 `scope`, accepting role names as bypasses, or converting an admin `sub` to customer identity would change the permission and ownership contracts.
- **Sources**: [Auth0 permissions-claim setup](https://support.auth0.com/center/s/article/How-to-Add-the-Permissions-Claim-to-an-Access-Token), [Auth0 access-token validation](https://auth0.com/docs/secure/tokens/access-tokens/validate-access-tokens), `internal/delivery/http/middleware/auth.go`.

## 5. Route policy and denial contract

- **Decision**: Chain authentication then one exact permission middleware on each of the 34 admin routes. Keep five category/product GET routes public even with a bad Authorization header and keep four customer review routes on their present middleware. Return `401 UNAUTHENTICATED` for credential failures and `403 FORBIDDEN` for valid tokens without the assigned permission, with `{"error":{"code":"...","message":"...","details":{}}}` and safe fixed messages.
- **Rationale**: The spec's 43-route inventory is the complete policy for current business routes. Per-route chaining is visible in review and prevents group middleware from accidentally protecting public or customer endpoints.
- **Alternatives considered**: Group-wide admin middleware, defaulting all GETs public, or a wildcard route rule could misclassify adjacent static and parameterized paths. Explicit router registration plus a route-policy test is safer.
- **Sources**: `specs/006-admin-auth0-authorization/spec.md`, `internal/delivery/http/router.go`, `internal/delivery/http/middleware/auth.go`.

## 6. Configuration and operational visibility

- **Decision**: Validate Auth0 domain and audience at startup, construct one verifier/cache, and inject it into routing. Record bounded outcomes (`authenticated`, `unauthenticated`, `forbidden`) with route template and method, using fixed metrics labels and safe logs. Update generated Swagger/API docs. No schema migration or business-use-case change is needed.
- **Rationale**: Explicit composition follows the constitution; startup validation avoids silently exposing routes with bad auth config. Route templates avoid high-cardinality identifiers, and no token/subject/claim value is logged.
- **Alternatives considered**: Runtime environment reads inside middleware, global verifier state, and logging raw parser errors are harder to test and risk disclosure.
- **Sources**: `.specify/memory/constitution.md`, `cmd/server/gin_server.go`, `internal/infrastructure/metrics/metrics.go`.
