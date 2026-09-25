package auth0

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JWKS cache policy. Auth0 recommends a 5-10 minute cache with a controlled
// refresh for an unrecognized key, so a rotation is picked up promptly while a
// forged or stale `kid` cannot drive a request storm.
const (
	defaultCacheTTL              = 5 * time.Minute
	defaultForcedRefreshInterval = 60 * time.Second
	defaultMaxJWKSBody           = 1 << 20 // 1 MiB
	defaultFetchTimeout          = 5 * time.Second
)

// JWK constants a compatible signing key must carry.
const (
	// RSAKeyType is the only accepted JWK `kty`.
	RSAKeyType = "RSA"
	// SigningAlgorithm is the only accepted JWK `alg` and JWT signing method.
	SigningAlgorithm = "RS256"
)

var (
	// errKeyUnavailable means no trusted, current key matched the request. The
	// caller fails closed; a key from a stale set is never substituted.
	errKeyUnavailable = errors.New("no current JWKS key matches the token")
	// errRefreshRateLimited means an unknown kid asked for a refresh inside the
	// bounded interval, so no further fetch was made.
	errRefreshRateLimited = errors.New("JWKS refresh is rate limited")
)

// jsonWebKey is one entry of an Auth0 JWKS document.
type jsonWebKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// jwksDocument is the parsed body of a JWKS response.
type jwksDocument struct {
	Keys []jsonWebKey `json:"keys"`
}

// cacheOptions configures the process-local JWKS cache. Zero values fall back
// to the production defaults.
type cacheOptions struct {
	client                *http.Client
	now                   func() time.Time
	ttl                   time.Duration
	forcedRefreshInterval time.Duration
	maxBody               int64
}

// jwksCache is a concurrency-safe, process-local cache of Auth0 public signing
// keys. Only validated public material from the configured endpoint is stored,
// and a token never influences the fetch target.
type jwksCache struct {
	jwksURL               string
	client                *http.Client
	now                   func() time.Time
	ttl                   time.Duration
	forcedRefreshInterval time.Duration
	maxBody               int64

	// mu guards the cached set and its bookkeeping.
	mu                sync.Mutex
	keys              map[string]*rsa.PublicKey
	expiresAt         time.Time
	refreshedAt       time.Time
	lastForcedRefresh time.Time

	// fetchMu serializes network fetches so concurrent misses share one request.
	fetchMu sync.Mutex
}

// newJWKSCache builds a cache for one configured endpoint.
func newJWKSCache(jwksURL string, opts cacheOptions) *jwksCache {
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.client == nil {
		opts.client = &http.Client{Timeout: defaultFetchTimeout}
	}
	if opts.ttl <= 0 {
		opts.ttl = defaultCacheTTL
	}
	if opts.forcedRefreshInterval <= 0 {
		opts.forcedRefreshInterval = defaultForcedRefreshInterval
	}
	if opts.maxBody <= 0 {
		opts.maxBody = defaultMaxJWKSBody
	}

	return &jwksCache{
		jwksURL:               jwksURL,
		client:                opts.client,
		now:                   opts.now,
		ttl:                   opts.ttl,
		forcedRefreshInterval: opts.forcedRefreshInterval,
		maxBody:               opts.maxBody,
	}
}

// lookup returns the cached public key for kid. It performs no network I/O on a
// current cache hit, fetches keys when the set has expired or the kid is
// unknown, and fails closed when no current key can be established.
func (c *jwksCache) lookup(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if strings.TrimSpace(kid) == "" {
		return nil, errKeyUnavailable
	}
	if key, ok := c.currentKey(kid); ok {
		return key, nil
	}

	c.mu.Lock()
	observed := c.refreshedAt
	c.mu.Unlock()

	if err := c.reload(ctx, kid, observed); err != nil {
		// A refresh failure is survivable only while a matching key from the
		// previous set is still current; an expired set is never revived.
		if key, ok := c.currentKey(kid); ok {
			return key, nil
		}
		return nil, err
	}
	if key, ok := c.currentKey(kid); ok {
		return key, nil
	}
	return nil, errKeyUnavailable
}

// currentKey reports the cached key for kid while the set is still current.
func (c *jwksCache) currentKey(kid string) (*rsa.PublicKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.currentLocked(c.now()) {
		return nil, false
	}
	key, ok := c.keys[kid]
	return key, ok
}

// currentLocked reports whether the cached set is still usable at now.
func (c *jwksCache) currentLocked(now time.Time) bool {
	return len(c.keys) > 0 && now.Before(c.expiresAt)
}

// reload performs at most one coalesced fetch. Callers that waited on the fetch
// lock reuse the result the winner produced instead of issuing another request.
//
// An unknown kid on an otherwise current set is bounded to one attempt per
// interval; an expired set is always refreshed. A failed fetch publishes
// nothing, so a previous set stays authoritative only until it expires.
func (c *jwksCache) reload(ctx context.Context, kid string, observed time.Time) error {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	c.mu.Lock()
	now := c.now()
	if c.currentLocked(now) {
		if _, known := c.keys[kid]; known || c.refreshedAt.After(observed) {
			// Another caller already published the set while this one waited.
			c.mu.Unlock()
			return nil
		}
		if now.Sub(c.lastForcedRefresh) < c.forcedRefreshInterval {
			c.mu.Unlock()
			return errRefreshRateLimited
		}
		// Record the attempt before doing I/O so a failed refresh is still bounded.
		c.lastForcedRefresh = now
	}
	c.mu.Unlock()

	keys, err := c.fetch(ctx)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.keys = keys
	c.refreshedAt = c.now()
	c.expiresAt = c.refreshedAt.Add(c.ttl)
	c.mu.Unlock()

	return nil
}

// fetch retrieves and parses the configured JWKS endpoint with a bounded body
// and a finite client timeout.
func (c *jwksCache) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build JWKS request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.maxBody))
		return nil, fmt.Errorf("fetch JWKS: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read JWKS: %w", err)
	}
	if int64(len(body)) > c.maxBody {
		return nil, fmt.Errorf("read JWKS: response exceeds the %d byte limit", c.maxBody)
	}

	return parseJWKS(body)
}

// parseJWKS turns a JWKS document into an exact kid -> RSA public key map,
// rejecting unusable or ambiguous documents rather than partially trusting them.
func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var document jwksDocument
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&document); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	if len(document.Keys) == 0 {
		return nil, errors.New("decode JWKS: no keys published")
	}

	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for i, jwk := range document.Keys {
		key, err := jwk.publicKey()
		if err != nil {
			return nil, fmt.Errorf("JWKS key %d: %w", i, err)
		}
		if _, duplicate := keys[jwk.Kid]; duplicate {
			return nil, fmt.Errorf("JWKS key %d: duplicate kid", i)
		}
		keys[jwk.Kid] = key
	}

	return keys, nil
}

// publicKey validates one JWK's metadata and material.
func (k jsonWebKey) publicKey() (*rsa.PublicKey, error) {
	switch {
	case k.Kty != RSAKeyType:
		return nil, fmt.Errorf("unsupported kty %q", k.Kty)
	case k.Kid == "":
		return nil, errors.New("missing kid")
	case k.Use != "" && k.Use != "sig":
		return nil, fmt.Errorf("unsupported use %q", k.Use)
	case k.Alg != "" && k.Alg != SigningAlgorithm:
		return nil, fmt.Errorf("unsupported alg %q", k.Alg)
	case k.N == "":
		return nil, errors.New("missing RSA modulus")
	case k.E == "":
		return nil, errors.New("missing RSA exponent")
	}

	modulus, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil || len(modulus) == 0 {
		return nil, errors.New("malformed RSA modulus")
	}

	exponentBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil || len(exponentBytes) == 0 {
		return nil, errors.New("malformed RSA exponent")
	}
	exponent := new(big.Int).SetBytes(exponentBytes)
	if !exponent.IsInt64() {
		return nil, errors.New("RSA exponent is out of range")
	}
	if e := exponent.Int64(); e < 2 || e > math.MaxInt32 {
		return nil, errors.New("RSA exponent is out of range")
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulus),
		E: int(exponent.Int64()),
	}, nil
}
