# Feature Specification: Admin Auth0 Authorization

**Feature Branch**: development (existing branch; no branch hook configured)
**Created**: 2026-09-24
**Status**: Draft
**Input**: Build separate Auth0 access-token authentication and per-endpoint permission checks for catalog and inventory administration while preserving customer review authentication and the existing error contract.

## Clarifications

### Session 2026-09-24

- Q: Should existing category and product GET routes remain public, require Auth0 read permissions, or be split? → A: Keep the existing GET routes public without a token requirement.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Perform an Authorized Management Action (Priority: P1)

An internal staff member presents an Auth0 access token for this service and performs a catalog or inventory action for which they have the exact granted permission.

**Why this priority**: Staff need access to their assigned work without acquiring unrelated powers.

**Independent Test**: For each protected management route, call it with a valid Auth0 access token containing its mapped permission and verify the request reaches the existing operation.

**Acceptance Scenarios**:

1. **Given** an admin has a valid token with products:update, **when** they update a product, **then** the normal product update outcome is returned.
2. **Given** an admin token has several permissions, **when** the caller invokes a protected route, **then** it succeeds if the route's one required permission is present.
3. **Given** an admin has inventory:reservations:create, **when** they create a valid reservation, **then** the existing reservation operation proceeds under its normal business rules.

---

### User Story 2 - Reject Unauthenticated or Unauthorized Management (Priority: P1)

A caller without acceptable admin credentials cannot read protected administration data or change catalog or inventory state, and receives an error that distinguishes authentication from permission failure.

**Why this priority**: Route protection prevents accidental exposure and privilege escalation.

**Independent Test**: Exercise every protected route with no token, invalid tokens, a valid token lacking the required permission, and a valid token with it; compare status, error shape, and side effects.

**Acceptance Scenarios**:

1. **Given** a protected route, **when** the bearer token is missing, malformed, expired, forged, from another issuer, or intended for another API, **then** HTTP 401 UNAUTHENTICATED is returned and no protected operation runs.
2. **Given** a valid admin token without the exact route permission, **when** the caller invokes that route, **then** HTTP 403 FORBIDDEN is returned and no protected operation runs.
3. **Given** a valid token with an empty or absent permissions list, **when** it calls a protected route, **then** it receives HTTP 403.
4. **Given** an access denial, **when** the response is returned, **then** it contains error.code, a safe error.message, and an error.details object without token content.

---

### User Story 3 - Preserve Customer Review Actions (Priority: P1)

A storefront customer continues to use their existing customer token to create, edit, or delete their own review and retrieve their own reviews. Admin tokens do not become a substitute for customer identity.

**Why this priority**: The two identity systems serve different users and ownership rules.

**Independent Test**: Repeat the current customer review authentication and ownership cases with a valid customer token, no token, and an Auth0 admin token.

**Acceptance Scenarios**:

1. **Given** a valid customer token, **when** its owner creates, updates, deletes, or lists their own reviews, **then** existing behavior and ownership checks remain in effect.
2. **Given** only an Auth0 admin token, **when** the caller invokes a customer-authenticated review action, **then** it does not grant customer identity or bypass ownership checks.
3. **Given** only a customer token, **when** the caller invokes an admin route, **then** HTTP 401 is returned.

---

### User Story 4 - Browse the Public Catalog (Priority: P2)

Storefront visitors browse the existing category and product read routes without signing in. These routes remain available whether or not a caller has an admin permission.

**Why this priority**: Public browsing is an existing storefront journey and must continue through the same routes.

**Independent Test**: Call every category and product GET route without a token, with a customer token, and with an Auth0 token that lacks the corresponding read permission; compare each result with the route's existing catalog behavior.

**Acceptance Scenarios**:

1. **Given** a storefront visitor has no token, **when** they list categories or products, use the category dropdown, or get a category or product by identifier or slug, **then** the existing catalog response is available.
2. **Given** a caller has an Auth0 token without categories:read or products:read, **when** they call one of these public GET routes, **then** the missing permission does not cause a 403.
3. **Given** a caller sends an invalid bearer token to one of these public GET routes, **when** the request is handled, **then** the token is not validated and does not block public catalog access.

### Edge Cases

- A bearer header has the wrong scheme, missing token text, or ambiguous multiple credentials.
- A token has an invalid signature or unsupported signing method, wrong issuer or audience, expired validity, or a future validity start.
- A signed token has no permissions claim, an empty list, duplicate grants, or a malformed permission value. Missing or empty grants authorize nothing; a malformed permission claim makes the token invalid.
- A token contains a similar but different permission; only the exact assigned string grants access.
- A customer token is submitted to an admin route, or an admin token is submitted to a customer-owned review route.
- Identity-provider verification material is unavailable or invalid; protected operations fail closed and no credential is logged.
- An unknown route or HTTP method must not inherit authorization from a neighboring route.

## Scope

The existing customer review path is separate from admin authorization. Health, readiness, metrics, and API documentation are operational endpoints outside this management permission map. Each current business route has an explicit policy below.

| Permission or policy | Existing routes covered |
| --- | --- |
| categories:create | POST /api/v1/categories |
| Public catalog read; categories:read not required | GET /api/v1/categories; GET /api/v1/categories/:id; GET /api/v1/categories/dropdown |
| categories:update | PUT /api/v1/categories/:id |
| categories:delete | DELETE /api/v1/categories/:id |
| products:create | POST /api/v1/products |
| Public catalog read; products:read not required | GET /api/v1/products; GET /api/v1/products/:id (identifier or slug) |
| products:update | PATCH /api/v1/products/:id |
| products:delete | DELETE /api/v1/products/:id (archive) |
| products:restore | POST /api/v1/products/:id/restore |
| products:purge | DELETE /api/v1/products/:id/purge |
| products:options:create | POST /api/v1/products/:id/options; POST /api/v1/products/:id/options/:option_id/values |
| products:options:update | PATCH /api/v1/products/:id/options/reorder; PATCH /api/v1/products/:id/options/:option_id; PATCH /api/v1/products/:id/options/:option_id/values/:value_id |
| products:options:delete | DELETE /api/v1/products/:id/options/:option_id; DELETE /api/v1/products/:id/options/:option_id/values/:value_id |
| products:variants:create | POST /api/v1/products/:id/variants; POST /api/v1/products/:id/variants/bulk |
| products:variants:update | PATCH /api/v1/products/:id/variants/bulk; PATCH /api/v1/products/:id/variants/reorder |
| products:variants:delete | POST /api/v1/products/:id/variants/bulk-delete |
| products:media:create | POST /api/v1/products/:id/media |
| products:media:update | PATCH /api/v1/products/:id/media/reorder; PATCH /api/v1/products/:id/media/:media_id |
| products:media:delete | DELETE /api/v1/products/:id/media/:media_id |
| products:reviews:read | GET /api/v1/products/:id/reviews; GET /api/v1/products/:id/reviews/summary |
| variants:update | PATCH /api/v1/variants/:id |
| variants:delete | DELETE /api/v1/variants/:id |
| variants:restore | POST /api/v1/variants/:id/restore |
| variants:media:create | POST /api/v1/variants/:id/media |
| variants:media:update | PATCH /api/v1/variants/:id/media/reorder |
| variants:media:delete | DELETE /api/v1/variants/:id/media/:media_id |
| reviews:read | GET /api/v1/reviews/:reviewId |
| inventory:reservations:create | POST /api/v1/inventory/reservations |
| Existing customer authentication; no Auth0 permission | POST /api/v1/products/:id/reviews; PATCH /api/v1/reviews/:reviewId; DELETE /api/v1/reviews/:reviewId; GET /api/v1/users/me/reviews |

The five existing category and product GET routes remain public and do not validate an Auth0 token or require categories:read or products:read. Those grants remain defined in Auth0 but are unused by the current routes. This feature does not introduce separate admin-preview read routes.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A protected admin route MUST accept an Auth0-issued bearer access token only when it is authentic, issued by the configured trusted issuer for this API, valid at request time, and intended for the configured API audience.
- **FR-002**: For each admin-protected method and route in the scope table, the service MUST determine access from the token's granted permissions and require the exact one assigned permission. Other permissions MUST NOT imply it.
- **FR-003**: Every admin-management route in the scope table MUST enforce its permission before invoking its existing operation. A newly added management route MUST receive an explicit access policy before exposure.
- **FR-004**: Missing, malformed, invalid, expired, or wrong-audience admin credentials MUST produce HTTP 401 with error.code = UNAUTHENTICATED; an authentic valid admin token lacking the assigned permission MUST produce HTTP 403 with error.code = FORBIDDEN.
- **FR-005**: Each denial in FR-004 MUST include error.message and error.details alongside error.code, without disclosing token contents, identity-provider secrets, or sensitive validation internals. A denial MUST cause no protected business operation or write.
- **FR-006**: Customer review creation, author updates and deletes, and the current user's review listing MUST keep existing customer-token authentication, identity, and ownership behavior. They MUST NOT require or accept an Auth0 permission as a substitute for customer identity.
- **FR-007**: Product-review listing and summary and review-by-id access MUST use the admin read permissions shown in the table. Customer-owned review actions remain governed by FR-006.
- **FR-008**: All five existing category and product GET routes MUST remain public without requiring or validating an Auth0 token. The service MUST NOT require categories:read or products:read on those routes.
- **FR-009**: Authorization failures MUST be distinguishable in operational records by outcome and route, without recording bearer tokens or other credentials.
- **FR-010**: This feature MUST preserve existing outcomes, payloads, persistence rules, and concurrency behavior of catalog, review, variant, media, and inventory operations once a request passes access checks.

### Key Entities

- **Admin Access Token**: An Auth0-issued credential with issuer, intended audience, validity period, and granted permission strings for an internal staff member.
- **Route Access Policy**: The association of a method and route with one admin permission, existing customer authentication, or deliberate public access.
- **Permission Grant**: An exact resource and action name in the admin token that authorizes a protected route.
- **Customer Identity**: The existing storefront review author's identity and ownership context, separate from admin identity.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In route-level acceptance checks, 100% of protected management routes allow a valid admin token with the assigned permission to reach the existing operation and reject a valid token without it with HTTP 403 FORBIDDEN.
- **SC-002**: In route-level acceptance checks, 100% of protected management routes reject missing, malformed, invalid, and expired tokens with HTTP 401 UNAUTHENTICATED and make no business change.
- **SC-003**: All 43 current business routes have an explicit policy: 34 require one admin permission, five category or product GET routes are public, and four review routes use existing customer authentication. Operational routes are documented separately.
- **SC-004**: Existing customer review creation, author edit and delete, and own-review journeys pass their pre-feature acceptance cases with the same customer-token and ownership outcomes; no Auth0 token alone completes one of these journeys.
- **SC-005**: In an acceptance exercise covering all protected admin actions, at least 95% of authorized staff actions complete without an additional sign-in prompt, and every access denial gives the caller the documented 401 or 403 error shape.

## Assumptions

- Exactly one named permission is required per protected admin route. A token may contain several grants, but there are no multi-permission AND rules or role-based bypasses in this version. The five public catalog GET routes have no permission requirement.
- An admin role or superadmin designation does not bypass the route's named permission; Auth0 role assignment must yield the required permission grant.
- Token validity is based on authenticity, configured issuer and audience, and validity times. Expiration is enforced at its stated time without an additional grace period; a revoked-token deny-list or online revocation check is outside this first version.
- Adding an option value uses products:options:create because the action creates a new value. Changing an existing value uses products:options:update.
- The products:reviews:create and users:reviews:read labels in the supplied permission catalog do not grant Auth0 access to customer-owned routes. Customer review author update and delete likewise remain on the customer identity path.
- This policy supersedes the earlier reservation specification's assumption that POST /api/v1/inventory/reservations follows a general internal-service authentication policy; the reservation's business contract remains intact.
- Auth0 is already configured to issue access tokens for this API with the stated permission names. Admin sign-in UI, staff provisioning, and permission assignment in Auth0 are outside this feature.
- Access checks do not create a persistent business entity or database write; protected operations retain their existing consistency and concurrency behavior.
