package auth0

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gin-product-service/internal/domain"

	"github.com/golang-jwt/jwt/v5"
)

// Option configures an Auth0 admin token verifier.
type Option func(*verifierOptions)

// verifierOptions collects the injectable collaborators. Only the JWKS URL
// override changes the trust boundary, and it is still deployment
// configuration: no token input can reach it.
type verifierOptions struct {
	client                *http.Client
	now                   func() time.Time
	jwksURL               string
	ttl                   time.Duration
	forcedRefreshInterval time.Duration
	maxBody               int64
}

// WithHTTPClient sets the HTTP client used for bounded JWKS retrieval.
func WithHTTPClient(client *http.Client) Option {
	return func(o *verifierOptions) { o.client = client }
}

// WithClock sets the clock used for cache lifetime and token-time validation.
func WithClock(now func() time.Time) Option {
	return func(o *verifierOptions) { o.now = now }
}

// WithJWKSURL overrides the JWKS endpoint. The default is the endpoint derived
// from the configured Auth0 domain; an override is deployment configuration and
// is never taken from a token.
func WithJWKSURL(url string) Option {
	return func(o *verifierOptions) { o.jwksURL = url }
}

// WithCacheTTL sets how long a successfully fetched key set is trusted.
func WithCacheTTL(ttl time.Duration) Option {
	return func(o *verifierOptions) { o.ttl = ttl }
}

// WithForcedRefreshInterval bounds how often an unknown kid may force a refresh.
func WithForcedRefreshInterval(interval time.Duration) Option {
	return func(o *verifierOptions) { o.forcedRefreshInterval = interval }
}

// WithMaxJWKSBody bounds the accepted JWKS response size.
func WithMaxJWKSBody(maxBytes int64) Option {
	return func(o *verifierOptions) { o.maxBody = maxBytes }
}

// Verifier authenticates Auth0 RS256 access tokens against the configured
// tenant and reports the trusted admin identity behind them. It owns one
// process-local key cache and performs no network I/O at construction time.
type Verifier struct {
	config Config
	keys   *jwksCache
	now    func() time.Time
}

// NewVerifier validates the Auth0 configuration and builds a verifier. It never
// performs network I/O: key material is retrieved lazily on the first
// credential that needs it.
func NewVerifier(config Config, options ...Option) (*Verifier, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}

	opts := verifierOptions{}
	for _, apply := range options {
		apply(&opts)
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.jwksURL == "" {
		opts.jwksURL = config.JWKSURL()
	}

	return &Verifier{
		config: config,
		now:    opts.now,
		keys: newJWKSCache(opts.jwksURL, cacheOptions{
			client:                opts.client,
			now:                   opts.now,
			ttl:                   opts.ttl,
			forcedRefreshInterval: opts.forcedRefreshInterval,
			maxBody:               opts.maxBody,
		}),
	}, nil
}

// Verify authenticates rawToken and returns the admin identity it establishes.
//
// It accepts only an RS256 token signed by the key whose kid matches the token
// header, with the exact configured issuer, the configured audience, a required
// and strictly future `exp`, and a `nbf`/`iat` that is not in the future. Every
// failure is reported as domain.ErrAdminTokenInvalid; the wrapped cause exists
// only for operational diagnosis and is never returned to a caller.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (domain.AdminIdentity, error) {
	if strings.TrimSpace(rawToken) == "" {
		return domain.AdminIdentity{}, fmt.Errorf("%w: empty credential", domain.ErrAdminTokenInvalid)
	}

	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{SigningAlgorithm}),
		jwt.WithIssuer(v.config.Issuer()),
		jwt.WithAudience(v.config.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(0),
		jwt.WithTimeFunc(v.now),
	)

	token, err := parser.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if strings.TrimSpace(kid) == "" {
			return nil, errors.New("token is missing a kid header")
		}
		return v.keys.lookup(ctx, kid)
	})
	if err != nil {
		return domain.AdminIdentity{}, fmt.Errorf("%w: %w", domain.ErrAdminTokenInvalid, err)
	}
	if !token.Valid {
		return domain.AdminIdentity{}, fmt.Errorf("%w: token is not valid", domain.ErrAdminTokenInvalid)
	}

	permissions, err := parsePermissions(claims["permissions"])
	if err != nil {
		return domain.AdminIdentity{}, fmt.Errorf("%w: %w", domain.ErrAdminTokenInvalid, err)
	}

	// The subject is opaque: it is never interpreted as a customer identifier.
	subject, _ := claims["sub"].(string)

	return domain.AdminIdentity{Subject: subject, Permissions: permissions}, nil
}

// parsePermissions reads the `permissions` claim. An absent claim grants
// nothing; a present claim must be an array of non-blank strings, otherwise the
// credential is invalid. Duplicates are collapsed.
func parsePermissions(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("permissions claim is not an array")
	}

	seen := make(map[string]struct{}, len(list))
	permissions := make([]string, 0, len(list))
	for _, entry := range list {
		value, ok := entry.(string)
		if !ok {
			return nil, errors.New("permissions claim contains a non-string entry")
		}
		if strings.TrimSpace(value) == "" {
			return nil, errors.New("permissions claim contains a blank entry")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		permissions = append(permissions, value)
	}

	if len(permissions) == 0 {
		return nil, nil
	}
	return permissions, nil
}
