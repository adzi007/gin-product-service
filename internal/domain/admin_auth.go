package domain

import (
	"context"
	"errors"
)

// ErrAdminTokenInvalid is returned when an admin bearer credential cannot be
// authenticated: a bad signature, an unsupported algorithm, a wrong issuer or
// audience, an invalid validity window, an unknown or unavailable signing key,
// or a malformed permissions claim.
//
// Callers map any occurrence to HTTP 401 and must not expose the wrapped cause
// to the client. Internal causes exist only for operational diagnosis.
var ErrAdminTokenInvalid = errors.New("admin access token is invalid")

// AdminIdentity is the narrow, trusted result of verifying an Auth0 admin
// access token. It deliberately carries no customer identity.
type AdminIdentity struct {
	// Subject is the optional opaque Auth0 `sub` claim, kept only for request
	// context and diagnostics. It is empty when the verified token omits it and
	// must never be interpreted as a customer UUID.
	Subject string
	// Permissions holds the exact grants decoded from the verified
	// `permissions` claim, with duplicates removed. It is nil when the token
	// grants nothing.
	Permissions []string
}

// HasPermission reports whether the identity holds the exact permission.
// A similar or differently scoped grant does not imply it.
func (a AdminIdentity) HasPermission(permission string) bool {
	for _, granted := range a.Permissions {
		if granted == permission {
			return true
		}
	}
	return false
}

// AdminTokenVerifier establishes the trusted admin identity behind one bearer
// credential. Implementations must reject any credential whose signature,
// algorithm, issuer, audience, validity window, or signing key cannot be
// verified, and must never return an identity for an unverified token.
//
// This port keeps Gin, JWT, and Auth0 vocabulary out of the domain and delivery
// layers: authentication mechanics live in infrastructure, and the delivery
// middleware only consumes the trusted result.
type AdminTokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (AdminIdentity, error)
}
