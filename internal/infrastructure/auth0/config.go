package auth0

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidConfig marks an unusable Auth0 configuration. Startup must fail on
// it rather than expose management routes with unvalidated trust material.
var ErrInvalidConfig = errors.New("invalid Auth0 configuration")

// jwksPath is the fixed, well-known location of an Auth0 tenant's public keys.
const jwksPath = "/.well-known/jwks.json"

// Config is the validated Auth0 deployment configuration for this API. It holds
// no secret material: the admin trust boundary is asymmetric, and the trusted
// issuer and JWKS URL are derived from Domain alone.
type Config struct {
	// Domain is the Auth0 host name only, with no scheme and no path.
	Domain string
	// Audience is the exact Auth0 API identifier expected in a token's `aud`.
	Audience string
}

// NewConfig validates the Auth0 domain and API audience and returns the trusted
// configuration. A missing, misconfigured, or partially formed value is
// rejected rather than silently rewritten.
func NewConfig(domain, audience string) (Config, error) {
	cfg := Config{
		Domain:   strings.TrimSpace(domain),
		Audience: strings.TrimSpace(audience),
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate reports whether the configuration can be trusted for request
// verification. It is re-applied by the verifier so a zero-value Config cannot
// be used to build a permissive verifier.
func (c Config) validate() error {
	switch {
	case c.Domain == "":
		return fmt.Errorf("%w: AUTH0_DOMAIN is required", ErrInvalidConfig)
	case strings.ContainsAny(c.Domain, " \t\r\n"):
		return fmt.Errorf("%w: AUTH0_DOMAIN must be a host name without whitespace", ErrInvalidConfig)
	case strings.Contains(c.Domain, "://"):
		return fmt.Errorf("%w: AUTH0_DOMAIN must not include a scheme", ErrInvalidConfig)
	case strings.Contains(c.Domain, "@"):
		return fmt.Errorf("%w: AUTH0_DOMAIN must not include userinfo", ErrInvalidConfig)
	case strings.ContainsAny(c.Domain, "/?#"):
		return fmt.Errorf("%w: AUTH0_DOMAIN must not include a path, query, or fragment", ErrInvalidConfig)
	}

	if c.Audience == "" {
		return fmt.Errorf("%w: AUTH0_AUDIENCE is required", ErrInvalidConfig)
	}
	return nil
}

// Issuer is the exact `iss` value a trusted token must carry. It is derived only
// from the configured domain, never from token input.
func (c Config) Issuer() string { return "https://" + c.Domain + "/" }

// JWKSURL is the fixed public-key endpoint for the configured domain. It is
// derived only from the configured domain, so a token `jku`/`x5u` header can
// never redirect key retrieval.
func (c Config) JWKSURL() string { return "https://" + c.Domain + jwksPath }
