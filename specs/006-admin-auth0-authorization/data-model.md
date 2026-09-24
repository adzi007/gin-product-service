
# Data Model: Admin Auth0 Authorization

This feature adds no persistent entity, table, migration, or business write. These are runtime security values and relationships. The authoritative policy is [contracts/admin-authorization.md](contracts/admin-authorization.md).

## Admin Access Token

An external, Auth0-issued bearer credential. The service never stores its raw value.

| Field | Type | Validation and use |
| --- | --- | --- |
| `alg` | JOSE header string | Exactly `RS256`; any other algorithm is 401. |
| `kid` | JOSE header string | Nonempty and exactly matches one current JWKS signing key. No fallback to a different key. |
| `iss` | claim string | Exactly `https://{AUTH0_DOMAIN}/`. |
| `aud` | claim string or string array | Contains the exact configured `AUTH0_AUDIENCE`. |
| `exp` | NumericDate | Required and strictly later than verification time; no leeway. |
| `nbf` | optional NumericDate | If present, verification time must be at or after it. |
| `iat` | optional NumericDate | If present, cannot be in the future. |
| `sub` | optional string | Opaque admin subject for context/diagnostics only; never a customer UUID. |
| `permissions` | optional string array | Exact grants. Missing or empty means no grants; malformed entries invalidate the token. |

The verifier establishes `AuthenticatedAdmin` only after the signature and all required claims pass. A verified token cannot create customer identity.

## AuthenticatedAdmin

Ephemeral trusted result passed from the verifier to admin authentication middleware.

| Field | Type | Validation and use |
| --- | --- | --- |
| `Subject` | optional opaque string | Derived only from a verified token. Not used for review ownership. |
| `Permissions` | set of exact strings | Derived only from the verified `permissions` array; duplicates may be deduplicated. Never synthesized from roles or `scope`. |

The admin authentication middleware stores these values under namespaced Gin context keys. The permission middleware reads them after authentication. No value is persisted.

## Permission Grant and Route Access Policy

A `PermissionGrant` is a nonblank, exact string such as `products:update`. One protected `(HTTP method, registered route template)` maps to one grant. The policy also permits two non-admin classes:

- **Public catalog read**: No authentication or token validation. There are five existing GET routes.
- **Customer review identity**: Existing HS256 customer `RequireAuth` and ownership checks. There are four routes.

All 34 admin routes have one assigned permission in the contract. Unknown methods and routes gain no policy from an adjacent route; future business routes need an explicit decision. `categories:read` and `products:read` remain unused on the five public catalog reads.

## JWKS Key Cache

Process-local verification material, shared across requests by the server's one Auth0 verifier.

| Field | Type | Rule |
| --- | --- | --- |
| `keysByKID` | map of string to RSA public key | Built only from the configured HTTPS Auth0 JWKS endpoint; reject duplicate `kid`, invalid modulus/exponent, wrong `kty`, or incompatible signing metadata. |
| `expiresAt` | time | Five minutes after successful fetch. Expired entries are not trusted. |
| `lastForcedRefresh` | time | At most one unknown-`kid` forced refresh per 60 seconds. |
| `refreshInFlight` | synchronization state | Concurrent misses share one fetch; no token-controlled endpoint is used. |

**Transitions**:

1. `empty` -> `fresh` after a successful bounded fetch.
2. `fresh` -> `fresh` on a cache hit without network I/O.
3. `fresh` -> `refreshing` on expiry or an eligible unknown `kid`; success replaces the set atomically.
4. `refreshing` -> `fresh` on success. On failure, a previously fresh matching key may serve until its expiry; otherwise verification fails closed. An unknown `kid` never authorizes from a different key.
5. `fresh` -> `expired` at `expiresAt`; a failed fetch from `expired` does not revive old entries.

## Denial Outcome

A runtime result with status and safe API envelope, not a stored record.

| Condition | HTTP status | `error.code` | `error.message` | `error.details` |
| --- | --- | --- | --- | --- |
| Missing, ambiguous, malformed, expired, or unverifiable admin bearer token | 401 | `UNAUTHENTICATED` | Safe fixed text such as `invalid or missing access token` | `{}` |
| Valid admin token lacks the route's exact grant | 403 | `FORBIDDEN` | Safe fixed text such as `insufficient permission` | `{}` |

No denial invokes its protected business handler. Outcome and route template may be logged or counted, without credential or subject content.
