
# Implementation Plan: Admin Auth0 Authorization

**Branch**: `development` (feature directory `006-admin-auth0-authorization`) | **Date**: 2026-09-24 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/006-admin-auth0-authorization/spec.md` and the requested RS256/JWKS, middleware separation, error, and router constraints.

## Summary

Protect the 34 existing catalog, review-read, variant, and inventory management routes with an Auth0 RS256 access-token verifier followed by an exact permission check. Fetch public keys only from the configured Auth0 domain's JWKS endpoint, cache them, select the matching `kid`, and validate signature, issuer, audience, and token times. Preserve the existing customer HS256 `middleware.RequireAuth(jwtSecret)` and its four review routes exactly as a separate identity path. Keep five catalog GET routes public. Denials use the current `error.code`, `error.message`, `error.details` envelope.

## Technical Context

**Language/Version**: Go 1.25 (`go.mod`)

**Primary Dependencies**: Gin 1.12, `github.com/golang-jwt/jwt/v5` 5.3.1, Go `net/http` and `crypto/rsa`; no new auth dependency is required.

**Storage**: No database schema or persistent auth state. A process-local, concurrency-safe JWKS cache stores public RSA keys by `kid`. Existing business storage remains PostgreSQL; reservation coordination remains Redis.

**Testing**: `go test ./...`; deterministic verifier and middleware tests using a local JWKS HTTP server and generated RSA test keys; route-policy tests covering every registered business method/path; existing review, catalog, and reservation regression tests. No new SQL integration test is needed because no SQL changes are planned.

**Target Platform**: Existing Linux Go HTTP service.

**Project Type**: HTTP API service.

**Performance Goals**: No JWKS network request for a valid cache hit. A cold or refresh fetch must have a finite timeout, response-size limit, and concurrent-request coalescing. No numerical latency or throughput target is specified by the feature.

**Constraints**: Require RS256; derive the trusted HTTPS JWKS URL from `AUTH0_DOMAIN`, never from a JWT header; accept only the configured API audience and exact issuer `https://{AUTH0_DOMAIN}/`; require `exp`, validate `nbf` and `iat` when supplied, and allow no expiry grace period. `permissions` is a string array; missing/empty means no grants and malformed means invalid credentials. Never log credentials or expose verification internals. Auth0 configuration must be validated at startup; customer `API_JWT_SECRET` remains separate.

**Scale/Scope**: 43 current business routes: 34 Auth0-protected, five public catalog reads, four existing customer-authenticated review routes. Operational endpoints stay outside this policy.

## Constitution Check

*Gate before research: PASS. Re-evaluated after design: PASS.*

| Constitution rule | Design decision and verification |
| --- | --- |
| I. Domain-centered dependency flow | Place an admin identity/verifier port in `internal/domain`, an Auth0 JWKS adapter in `internal/infrastructure/auth0`, and Gin authentication/authorization middleware in a separate `internal/delivery/http/middleware/admin` package. Inject the verifier into router setup from `internal/wire`. Existing handlers and use cases remain unchanged. |
| II. Business integrity | Authorization ends before any existing operation. It creates no database write and changes no aggregate, transaction, reservation lock, or business invariant. |
| III. Explicit composition | Construct one verifier and shared cache in `internal/wire`, pass it explicitly through the container to `SetupRouter`, and chain authentication then permission middleware on each protected route. Do not create a package-global verifier. |
| IV. PostgreSQL correctness | No SQL, migration, or persistence changes. Existing pgx/v5 paths remain authoritative. |
| V. Verification and visibility | Test RS256 validation, key rotation/cache failures, exact permissions, error envelopes, customer isolation, public reads, and all route policies. Emit bounded outcome/route logs and metrics without tokens, subjects, or unbounded claim labels. Update generated Swagger/API docs for authorization responses. |

No constitution deviation or migration is required. The new domain port represents the credential-verification boundary; Auth0 details stay in the adapter. Post-design review confirms the boundary and error/observability requirements are covered by [research.md](research.md), [data-model.md](data-model.md), [contracts/admin-authorization.md](contracts/admin-authorization.md), and [quickstart.md](quickstart.md).

## Project Structure

### Documentation (this feature)

```text
specs/006-admin-auth0-authorization/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── contracts/
    └── admin-authorization.md
```

`tasks.md` belongs to a later `$speckit-tasks` phase.

### Source Code (repository root)

```text
internal/domain/                         # narrow admin identity/verifier contract
internal/infrastructure/auth0/            # Auth0 RS256 verifier and JWKS cache
internal/delivery/http/middleware/admin/  # new RequireAuth and RequirePermission pair
internal/delivery/http/middleware/auth.go # existing customer HS256 middleware, unmodified
internal/delivery/http/router.go           # explicit per-route middleware chains
internal/delivery/http/router_routes_test.go
internal/infrastructure/metrics/metrics.go # bounded authorization outcome metric
internal/wire/container.go                  # one verifier/cache in the composition root
cmd/server/gin_server.go                   # startup config validation and router injection
docs/                                      # generated API documentation
```

**Structure Decision**: Keep authentication at the HTTP delivery boundary and public-key retrieval in infrastructure. The admin package's `RequireAuth` name is separate from the existing customer `middleware.RequireAuth`; router code can alias the admin package and use `adminAuth := admin.RequireAuth(verifier)` followed by `admin.RequirePermission("<permission>")`. The domain port keeps infrastructure from leaking into delivery and does not add an auth use case or persistence model.

## Implementation Sequence

1. Define the narrow admin verifier/identity contract and validated Auth0 configuration (`AUTH0_DOMAIN`, `AUTH0_AUDIENCE`). Keep issuer and JWKS URL derived from the configured domain. Reject missing or malformed config before serving; keep `API_JWT_SECRET` for customer routes.
2. Implement the Auth0 adapter with a bounded HTTPS JWKS fetch, process-local five-minute cache, refresh on unknown `kid` with rate limiting and request coalescing, RSA JWK parsing, and exact `kid` selection. Reject unknown/duplicate/incompatible keys, bad JWKS, unavailable required keys, and unsupported algorithms. Cache only public key material.
3. Verify RS256 signature and claims (`iss`, configured `aud` including array membership, required `exp`, optional `nbf`/`iat` with no leeway), then parse `permissions` as an array of strings. Missing or empty array becomes no grants; malformed array is 401. Treat `sub` as an opaque admin subject, never as a customer UUID.
4. Add the separate admin `RequireAuth` and `RequirePermission` middleware. Authentication sets admin permissions and optional subject in namespaced Gin context keys only after successful verification. Authorization reads that context and returns 403 for a missing exact permission. Both abort before handlers and write the existing safe error envelope. Reject absent, malformed, and ambiguous bearer headers.
5. Build one verifier in `internal/wire`, validate its configuration at startup in `cmd/server`, and inject the container-held verifier into `SetupRouter`. Chain `adminAuth, admin.RequirePermission("<permission>")` before each of the 34 handlers in the contract. Leave the five category/product GET registrations without admin middleware and the four customer routes using only existing `middleware.RequireAuth(jwtSecret)`.
6. Add bounded authorization outcome metrics and route-template logs, plus Swagger documentation for the new security policy and 401/403 envelopes. Verify the policy matrix and old behavior with focused and full Go tests. No transaction, migration, or business handler change is planned.

## Operational and Rollout Notes

- Auth0 API configuration must issue RS256 access tokens for `AUTH0_AUDIENCE` and include the `permissions` claim when its **Add Permissions in the Access Token** option is enabled. Auth0 `scope` is not a substitute for this contract.
- Deploy `AUTH0_DOMAIN` as a hostname and `AUTH0_AUDIENCE` as the exact Auth0 API identifier before enabling these protected routes. The resulting issuer is `https://{AUTH0_DOMAIN}/` and JWKS URL is `https://{AUTH0_DOMAIN}/.well-known/jwks.json`; a custom domain must be used consistently with token `iss`.
- The five public GET routes continue to ignore a supplied bearer token. Auth0 outage or key refresh failure fails closed for requests that need an uncached/expired key; no protected operation runs.
- No database migration or data backfill is necessary. The only external dependency added to request handling is Auth0 JWKS retrieval on cache miss or refresh.

## Complexity Tracking

No constitution violations require justification.
