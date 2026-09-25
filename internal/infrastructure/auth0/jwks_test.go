package auth0

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Shared test fixtures (also used by verifier_test.go)
// ---------------------------------------------------------------------------

// jwkFixture is one JWK document entry. Empty fields are omitted so a missing
// `kid`, `use`, or `alg` can be expressed.
type jwkFixture struct {
	Kty string `json:"kty"`
	Kid string `json:"kid,omitempty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
}

// generateRSAKey returns a fresh RSA key pair for a test signer.
func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	return key
}

// rsaJWK renders a public key as a compatible signing JWK.
func rsaJWK(kid string, key *rsa.PublicKey) jwkFixture {
	return jwkFixture{
		Kty: RSAKeyType,
		Kid: kid,
		Use: "sig",
		Alg: SigningAlgorithm,
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

// buildJWKS marshals JWK entries into a JWKS document.
func buildJWKS(t *testing.T, entries ...jwkFixture) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		Keys []jwkFixture `json:"keys"`
	}{Keys: entries})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	return raw
}

// jwksEndpoint is a local TLS JWKS endpoint with a request counter and a
// swappable response, so cache behavior is exercised without reaching Auth0.
type jwksEndpoint struct {
	server *httptest.Server

	mu       sync.Mutex
	requests int
	response []byte
	status   int
	delay    time.Duration
}

func newJWKSEndpoint(t *testing.T, response []byte) *jwksEndpoint {
	t.Helper()

	e := &jwksEndpoint{status: http.StatusOK, response: response}
	e.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e.mu.Lock()
		e.requests++
		status, body, delay := e.status, e.response, e.delay
		e.mu.Unlock()

		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(e.server.Close)

	return e
}

func (e *jwksEndpoint) url() string { return e.server.URL + jwksPath }

func (e *jwksEndpoint) client() *http.Client { return e.server.Client() }

func (e *jwksEndpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.requests
}

func (e *jwksEndpoint) setResponse(response []byte, status int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.response, e.status = response, status
}

func (e *jwksEndpoint) setDelay(delay time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.delay = delay
}

// testClock is a manually advanced clock for deterministic cache expiry.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestCache builds a cache pointed at a local endpoint with fixed policy.
func newTestCache(endpoint *jwksEndpoint, clock *testClock) *jwksCache {
	return newJWKSCache(endpoint.url(), cacheOptions{
		client:                endpoint.client(),
		now:                   clock.Now,
		ttl:                   defaultCacheTTL,
		forcedRefreshInterval: defaultForcedRefreshInterval,
		maxBody:               defaultMaxJWKSBody,
	})
}

// ---------------------------------------------------------------------------
// Key selection and parsing
// ---------------------------------------------------------------------------

func TestJWKSCacheSelectsExactKid(t *testing.T) {
	keyA := generateRSAKey(t)
	keyB := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t,
		rsaJWK("kid-a", &keyA.PublicKey),
		rsaJWK("kid-b", &keyB.PublicKey),
	))
	cache := newTestCache(endpoint, newTestClock())

	got, err := cache.lookup(context.Background(), "kid-b")
	if err != nil {
		t.Fatalf("lookup(kid-b) error = %v, want nil", err)
	}
	if got.N.Cmp(keyB.PublicKey.N) != 0 {
		t.Fatalf("lookup(kid-b) returned the wrong key")
	}

	// An unknown kid never falls back to a different cached key.
	if _, err := cache.lookup(context.Background(), "kid-missing"); err == nil {
		t.Fatal("lookup(kid-missing) error = nil, want an error")
	}
}

func TestJWKSCacheRejectsIncompatibleAndDuplicateKeys(t *testing.T) {
	valid := generateRSAKey(t)
	other := generateRSAKey(t)

	tests := []struct {
		name        string
		entries     []jwkFixture
		wantErr     bool
		noKeysAtAll bool
	}{
		{name: "no keys", entries: nil, wantErr: true},
		{name: "unsupported key type", entries: []jwkFixture{{Kty: "EC", Kid: "kid-a", Use: "sig", Alg: "ES256", N: "", E: ""}}, wantErr: true},
		{name: "missing kid", entries: []jwkFixture{{Kty: RSAKeyType, Use: "sig", Alg: SigningAlgorithm, N: base64.RawURLEncoding.EncodeToString(valid.PublicKey.N.Bytes()), E: "AQAB"}}, wantErr: true},
		{name: "duplicate kid", entries: []jwkFixture{rsaJWK("kid-a", &valid.PublicKey), rsaJWK("kid-a", &other.PublicKey)}, wantErr: true},
		{name: "unsupported use", entries: []jwkFixture{{Kty: RSAKeyType, Kid: "kid-a", Use: "enc", Alg: SigningAlgorithm, N: base64.RawURLEncoding.EncodeToString(valid.PublicKey.N.Bytes()), E: "AQAB"}}, wantErr: true},
		{name: "unsupported algorithm", entries: []jwkFixture{{Kty: RSAKeyType, Kid: "kid-a", Use: "sig", Alg: "HS256", N: base64.RawURLEncoding.EncodeToString(valid.PublicKey.N.Bytes()), E: "AQAB"}}, wantErr: true},
		{name: "malformed modulus", entries: []jwkFixture{{Kty: RSAKeyType, Kid: "kid-a", Use: "sig", Alg: SigningAlgorithm, N: "not base64!!", E: "AQAB"}}, wantErr: true},
		{name: "missing exponent", entries: []jwkFixture{{Kty: RSAKeyType, Kid: "kid-a", Use: "sig", Alg: SigningAlgorithm, N: base64.RawURLEncoding.EncodeToString(valid.PublicKey.N.Bytes())}}, wantErr: true},
		{name: "non-positive exponent", entries: []jwkFixture{{Kty: RSAKeyType, Kid: "kid-a", Use: "sig", Alg: SigningAlgorithm, N: base64.RawURLEncoding.EncodeToString(valid.PublicKey.N.Bytes()), E: "AA"}}, wantErr: true},
		{name: "compatible key", entries: []jwkFixture{rsaJWK("kid-a", &valid.PublicKey)}, wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := newJWKSEndpoint(t, buildJWKS(t, tc.entries...))
			cache := newTestCache(endpoint, newTestClock())

			key, err := cache.lookup(context.Background(), "kid-a")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("lookup() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("lookup() error = %v, want nil", err)
			}
			if key == nil {
				t.Fatal("lookup() key = nil, want a key")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cache lifetime and refresh bounds
// ---------------------------------------------------------------------------

func TestJWKSCacheServesFreshHitWithoutNetworkIO(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	clock := newTestClock()
	cache := newTestCache(endpoint, clock)

	if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
		t.Fatalf("first lookup() error = %v", err)
	}
	if got := endpoint.count(); got != 1 {
		t.Fatalf("fetches after first lookup = %d, want 1", got)
	}

	clock.Advance(defaultCacheTTL - time.Second)
	for i := 0; i < 5; i++ {
		if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
			t.Fatalf("cached lookup() error = %v", err)
		}
	}
	if got := endpoint.count(); got != 1 {
		t.Fatalf("fetches during cache lifetime = %d, want 1 (no network I/O on a fresh hit)", got)
	}
}

func TestJWKSCacheRefetchesAfterExpiry(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	clock := newTestClock()
	cache := newTestCache(endpoint, clock)

	if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
		t.Fatalf("first lookup() error = %v", err)
	}

	clock.Advance(defaultCacheTTL + time.Second)
	if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
		t.Fatalf("lookup() after expiry error = %v", err)
	}
	if got := endpoint.count(); got != 2 {
		t.Fatalf("fetches after expiry = %d, want 2", got)
	}
}

func TestJWKSCacheBoundsUnknownKidRefresh(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	clock := newTestClock()
	cache := newTestCache(endpoint, clock)

	if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
		t.Fatalf("lookup(kid-a) error = %v", err)
	}
	if got := endpoint.count(); got != 1 {
		t.Fatalf("initial fetches = %d, want 1", got)
	}

	// The first unknown kid forces exactly one refresh.
	if _, err := cache.lookup(context.Background(), "kid-unknown"); err == nil {
		t.Fatal("lookup(kid-unknown) error = nil, want an error")
	}
	if got := endpoint.count(); got != 2 {
		t.Fatalf("fetches after one unknown kid = %d, want 2", got)
	}

	// Further unknown kids inside the interval are refused without another fetch.
	for i := 0; i < 3; i++ {
		if _, err := cache.lookup(context.Background(), "kid-unknown"); err == nil {
			t.Fatal("rate-limited lookup() error = nil, want an error")
		}
	}
	if got := endpoint.count(); got != 2 {
		t.Fatalf("fetches inside the refresh interval = %d, want 2 (bounded)", got)
	}

	// Once the interval elapses one more refresh is permitted.
	clock.Advance(defaultForcedRefreshInterval + time.Second)
	if _, err := cache.lookup(context.Background(), "kid-unknown"); err == nil {
		t.Fatal("lookup() after the refresh interval error = nil, want an error")
	}
	if got := endpoint.count(); got != 3 {
		t.Fatalf("fetches after the refresh interval = %d, want 3", got)
	}
}

func TestJWKSCacheCoalescesConcurrentFetches(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	endpoint.setDelay(50 * time.Millisecond)
	cache := newTestCache(endpoint, newTestClock())

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
				t.Errorf("concurrent lookup() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := endpoint.count(); got != 1 {
		t.Fatalf("concurrent fetches = %d, want 1 (coalesced)", got)
	}
}

// ---------------------------------------------------------------------------
// Bounded transport and fail-closed behavior
// ---------------------------------------------------------------------------

func TestJWKSCacheRejectsOversizedResponse(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	cache := newJWKSCache(endpoint.url(), cacheOptions{
		client:                endpoint.client(),
		now:                   newTestClock().Now,
		ttl:                   defaultCacheTTL,
		forcedRefreshInterval: defaultForcedRefreshInterval,
		maxBody:               16,
	})

	if _, err := cache.lookup(context.Background(), "kid-a"); err == nil {
		t.Fatal("lookup() error = nil, want the oversized response to be rejected")
	}
}

func TestJWKSCacheTimesOutSlowFetch(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	endpoint.setDelay(500 * time.Millisecond)

	client := endpoint.client()
	client.Timeout = 50 * time.Millisecond

	cache := newJWKSCache(endpoint.url(), cacheOptions{
		client:                client,
		now:                   newTestClock().Now,
		ttl:                   defaultCacheTTL,
		forcedRefreshInterval: defaultForcedRefreshInterval,
		maxBody:               defaultMaxJWKSBody,
	})

	if _, err := cache.lookup(context.Background(), "kid-a"); err == nil {
		t.Fatal("lookup() error = nil, want the slow fetch to time out")
	}
}

func TestJWKSCacheFailsClosedOnOutage(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	clock := newTestClock()
	cache := newTestCache(endpoint, clock)

	if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
		t.Fatalf("warm-up lookup() error = %v", err)
	}

	// The provider outage starts while the cached set is still current: the
	// matching key keeps serving, and an unknown kid still fails.
	endpoint.setResponse(nil, http.StatusInternalServerError)
	if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
		t.Fatalf("lookup() during outage with a current key error = %v, want the cached key", err)
	}
	if _, err := cache.lookup(context.Background(), "kid-unknown"); err == nil {
		t.Fatal("lookup(kid-unknown) error = nil, want a fail-closed error")
	}

	// Once the set expires, a failed refresh must not revive the old keys.
	clock.Advance(defaultCacheTTL + time.Second)
	if _, err := cache.lookup(context.Background(), "kid-a"); err == nil {
		t.Fatal("lookup() of an expired set during outage error = nil, want fail-closed")
	}
}

func TestJWKSCacheRejectsNonSuccessStatus(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	endpoint.setResponse(nil, http.StatusForbidden)
	cache := newTestCache(endpoint, newTestClock())

	if _, err := cache.lookup(context.Background(), "kid-a"); err == nil {
		t.Fatal("lookup() error = nil, want a non-200 JWKS response to be rejected")
	}
}

func TestJWKSCacheRejectsUndecodableDocument(t *testing.T) {
	endpoint := newJWKSEndpoint(t, []byte("{not json"))
	cache := newTestCache(endpoint, newTestClock())

	if _, err := cache.lookup(context.Background(), "kid-a"); err == nil {
		t.Fatal("lookup() error = nil, want an undecodable JWKS to be rejected")
	}
}

// The cache only ever contacts the configured endpoint; a JWT-supplied URL
// cannot change the fetch target because no such input exists.
func TestJWKSCacheUsesConfiguredEndpointOnly(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	cache := newTestCache(endpoint, newTestClock())

	if cache.jwksURL != endpoint.url() {
		t.Fatalf("jwksURL = %q, want the configured endpoint %q", cache.jwksURL, endpoint.url())
	}
	if _, err := cache.lookup(context.Background(), "kid-a"); err != nil {
		t.Fatalf("lookup() error = %v", err)
	}
	if got := endpoint.count(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
}

func TestNewJWKSCacheAppliesDefaults(t *testing.T) {
	cache := newJWKSCache("https://tenant.us.auth0.com/.well-known/jwks.json", cacheOptions{})

	if cache.ttl != defaultCacheTTL {
		t.Errorf("ttl = %v, want %v", cache.ttl, defaultCacheTTL)
	}
	if cache.forcedRefreshInterval != defaultForcedRefreshInterval {
		t.Errorf("forcedRefreshInterval = %v, want %v", cache.forcedRefreshInterval, defaultForcedRefreshInterval)
	}
	if cache.maxBody != defaultMaxJWKSBody {
		t.Errorf("maxBody = %d, want %d", cache.maxBody, defaultMaxJWKSBody)
	}
	if cache.client == nil || cache.client.Timeout <= 0 {
		t.Errorf("client = %+v, want a client with a finite timeout", cache.client)
	}
	if cache.now == nil {
		t.Error("now = nil, want a clock")
	}
}

func TestJWKSCacheLookupRejectsBlankKid(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK("kid-a", &key.PublicKey)))
	cache := newTestCache(endpoint, newTestClock())

	if _, err := cache.lookup(context.Background(), "   "); err == nil {
		t.Fatal("lookup(blank) error = nil, want an error")
	}
	if got := endpoint.count(); got != 0 {
		t.Fatalf("fetches for a blank kid = %d, want 0", got)
	}
}
