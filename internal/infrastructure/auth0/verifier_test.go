package auth0

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"testing"
	"time"

	"gin-product-service/internal/domain"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testDomain   = "tenant.us.auth0.com"
	testAudience = "https://api.example.com/"
	testKid      = "kid-primary"
	testSubject  = "auth0|staff-42"
)

// authFixture wires one verifier to a local TLS JWKS endpoint holding a single
// generated RSA key, with a manually advanced clock.
type authFixture struct {
	verifier *Verifier
	endpoint *jwksEndpoint
	key      *rsa.PrivateKey
	clock    *testClock
	config   Config
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()

	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK(testKid, &key.PublicKey)))
	clock := newTestClock()

	cfg, err := NewConfig(testDomain, testAudience)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	verifier, err := NewVerifier(cfg,
		WithHTTPClient(endpoint.client()),
		WithClock(clock.Now),
		WithJWKSURL(endpoint.url()),
	)
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	return &authFixture{verifier: verifier, endpoint: endpoint, key: key, clock: clock, config: cfg}
}

// baseClaims returns a fully valid claim set relative to the fixture clock.
func (f *authFixture) baseClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":         f.config.Issuer(),
		"aud":         testAudience,
		"sub":         testSubject,
		"exp":         f.clock.Now().Add(time.Hour).Unix(),
		"iat":         f.clock.Now().Unix(),
		"permissions": []string{"products:reviews:read"},
	}
}

// signRS256 signs claims with the fixture key and the fixture kid.
func (f *authFixture) signRS256(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	return signWithKey(t, f.key, testKid, claims, jwt.SigningMethodRS256)
}

func signWithKey(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims, method jwt.SigningMethod) string {
	t.Helper()

	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return signed
}

// verify runs the verifier and reports whether it accepted the credential.
func (f *authFixture) verify(raw string) (domain.AdminIdentity, error) {
	return f.verifier.Verify(context.Background(), raw)
}

// ---------------------------------------------------------------------------
// Happy path
// ---------------------------------------------------------------------------

func TestVerifierAcceptsValidRS256Token(t *testing.T) {
	f := newAuthFixture(t)

	identity, err := f.verify(f.signRS256(t, f.baseClaims()))
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}
	if identity.Subject != testSubject {
		t.Errorf("Subject = %q, want %q", identity.Subject, testSubject)
	}
	if !identity.HasPermission("products:reviews:read") {
		t.Errorf("Permissions = %v, want products:reviews:read", identity.Permissions)
	}
}

// A valid token is verified from the cache, so repeated requests do not refetch
// the key material.
func TestVerifierReusesCachedKeys(t *testing.T) {
	f := newAuthFixture(t)
	token := f.signRS256(t, f.baseClaims())

	for i := 0; i < 3; i++ {
		if _, err := f.verify(token); err != nil {
			t.Fatalf("Verify() #%d error = %v", i, err)
		}
	}
	if got := f.endpoint.count(); got != 1 {
		t.Fatalf("JWKS fetches = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Key selection and signature
// ---------------------------------------------------------------------------

func TestVerifierRequiresMatchingKid(t *testing.T) {
	t.Run("unknown kid", func(t *testing.T) {
		f := newAuthFixture(t)
		token := signWithKey(t, f.key, "kid-somewhere-else", f.baseClaims(), jwt.SigningMethodRS256)

		if _, err := f.verify(token); err == nil {
			t.Fatal("Verify() error = nil, want an unknown kid to be rejected")
		}
	})

	t.Run("missing kid", func(t *testing.T) {
		f := newAuthFixture(t)
		token := signWithKey(t, f.key, "", f.baseClaims(), jwt.SigningMethodRS256)

		if _, err := f.verify(token); err == nil {
			t.Fatal("Verify() error = nil, want a missing kid to be rejected")
		}
	})

	t.Run("forged signature", func(t *testing.T) {
		f := newAuthFixture(t)
		attackerKey := generateRSAKey(t)
		// The attacker claims the trusted kid but cannot produce the signature.
		token := signWithKey(t, attackerKey, testKid, f.baseClaims(), jwt.SigningMethodRS256)

		if _, err := f.verify(token); err == nil {
			t.Fatal("Verify() error = nil, want a forged signature to be rejected")
		}
	})
}

func TestVerifierRejectsUnsupportedAlgorithms(t *testing.T) {
	t.Run("hs256", func(t *testing.T) {
		f := newAuthFixture(t)
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, f.baseClaims())
		token.Header["kid"] = testKid
		signed, err := token.SignedString([]byte("customer-secret"))
		if err != nil {
			t.Fatalf("SignedString() error = %v", err)
		}

		if _, err := f.verify(signed); err == nil {
			t.Fatal("Verify() error = nil, want HS256 to be rejected")
		}
	})

	t.Run("none", func(t *testing.T) {
		f := newAuthFixture(t)
		token := jwt.NewWithClaims(jwt.SigningMethodNone, f.baseClaims())
		token.Header["kid"] = testKid
		signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("SignedString() error = %v", err)
		}

		if _, err := f.verify(signed); err == nil {
			t.Fatal("Verify() error = nil, want alg=none to be rejected")
		}
	})

	t.Run("empty", func(t *testing.T) {
		f := newAuthFixture(t)
		if _, err := f.verify("   "); err == nil {
			t.Fatal("Verify() error = nil, want an empty credential to be rejected")
		}
	})

	t.Run("not a jwt", func(t *testing.T) {
		f := newAuthFixture(t)
		if _, err := f.verify("not-a-token"); err == nil {
			t.Fatal("Verify() error = nil, want an unparseable credential to be rejected")
		}
	})
}

// ---------------------------------------------------------------------------
// Claims
// ---------------------------------------------------------------------------

func TestVerifierRequiresExactIssuer(t *testing.T) {
	for _, issuer := range []string{
		"",
		"https://other.us.auth0.com/",
		"https://tenant.us.auth0.com",
		"http://tenant.us.auth0.com/",
	} {
		t.Run(issuer, func(t *testing.T) {
			f := newAuthFixture(t)
			claims := f.baseClaims()
			if issuer == "" {
				delete(claims, "iss")
			} else {
				claims["iss"] = issuer
			}

			if _, err := f.verify(f.signRS256(t, claims)); err == nil {
				t.Fatalf("Verify() error = nil for iss %q, want rejection", issuer)
			}
		})
	}
}

func TestVerifierRequiresConfiguredAudience(t *testing.T) {
	t.Run("string audience", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["aud"] = testAudience

		if _, err := f.verify(f.signRS256(t, claims)); err != nil {
			t.Fatalf("Verify() error = %v, want a string aud to be accepted", err)
		}
	})

	t.Run("array audience containing the api", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["aud"] = []string{"https://other.example.com/", testAudience}

		if _, err := f.verify(f.signRS256(t, claims)); err != nil {
			t.Fatalf("Verify() error = %v, want the configured audience in an array to be accepted", err)
		}
	})

	t.Run("array audience without the api", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["aud"] = []string{"https://other.example.com/"}

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want a foreign audience to be rejected")
		}
	})

	t.Run("wrong string audience", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["aud"] = "https://other.example.com/"

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want a foreign audience to be rejected")
		}
	})

	t.Run("missing audience", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		delete(claims, "aud")

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want a missing audience to be rejected")
		}
	})
}

func TestVerifierValidatesTokenTimes(t *testing.T) {
	t.Run("exp missing", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		delete(claims, "exp")

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want a missing exp to be rejected")
		}
	})

	t.Run("expired", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["exp"] = f.clock.Now().Add(-time.Second).Unix()

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want an expired token to be rejected")
		}
	})

	// No grace period: a token expiring exactly at the verification instant is
	// already invalid.
	t.Run("exp at the verification instant", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["exp"] = f.clock.Now().Unix()

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want zero expiry leeway to be enforced")
		}
	})

	t.Run("future nbf", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["nbf"] = f.clock.Now().Add(time.Minute).Unix()

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want a future nbf to be rejected")
		}
	})

	t.Run("future iat", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["iat"] = f.clock.Now().Add(time.Minute).Unix()

		if _, err := f.verify(f.signRS256(t, claims)); err == nil {
			t.Fatal("Verify() error = nil, want a future iat to be rejected")
		}
	})

	t.Run("current nbf and iat", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["nbf"] = f.clock.Now().Unix()
		claims["iat"] = f.clock.Now().Unix()

		if _, err := f.verify(f.signRS256(t, claims)); err != nil {
			t.Fatalf("Verify() error = %v, want a current nbf/iat to be accepted", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Subject and permissions
// ---------------------------------------------------------------------------

// The admin subject is opaque: a non-UUID subject is valid admin identity and is
// never reinterpreted as a customer id.
func TestVerifierTreatsSubjectAsOpaque(t *testing.T) {
	t.Run("non-uuid subject", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		claims["sub"] = "google-oauth2|109876543210987654321"

		identity, err := f.verify(f.signRS256(t, claims))
		if err != nil {
			t.Fatalf("Verify() error = %v, want a non-UUID subject to be accepted", err)
		}
		if identity.Subject != "google-oauth2|109876543210987654321" {
			t.Errorf("Subject = %q, want the raw opaque value", identity.Subject)
		}
	})

	t.Run("missing subject", func(t *testing.T) {
		f := newAuthFixture(t)
		claims := f.baseClaims()
		delete(claims, "sub")

		identity, err := f.verify(f.signRS256(t, claims))
		if err != nil {
			t.Fatalf("Verify() error = %v, want a missing subject to be accepted", err)
		}
		if identity.Subject != "" {
			t.Errorf("Subject = %q, want empty", identity.Subject)
		}
	})
}

func TestVerifierParsesPermissionsClaim(t *testing.T) {
	tests := []struct {
		name        string
		permissions any
		want        []string
		wantErr     bool
	}{
		{name: "missing claim grants nothing", permissions: nil, want: nil},
		{name: "empty array grants nothing", permissions: []string{}, want: nil},
		{name: "single grant", permissions: []string{"products:update"}, want: []string{"products:update"}},
		{
			name:        "several grants",
			permissions: []string{"products:update", "inventory:reservations:create"},
			want:        []string{"products:update", "inventory:reservations:create"},
		},
		{name: "duplicates are deduplicated", permissions: []string{"products:update", "products:update"}, want: []string{"products:update"}},
		{name: "malformed string claim", permissions: "products:update", wantErr: true},
		{name: "malformed object claim", permissions: map[string]string{"a": "b"}, wantErr: true},
		{name: "non-string entry", permissions: []any{"products:update", 7}, wantErr: true},
		{name: "blank entry", permissions: []any{"products:update", "   "}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthFixture(t)
			claims := f.baseClaims()
			if tc.permissions == nil {
				delete(claims, "permissions")
			} else {
				claims["permissions"] = tc.permissions
			}

			identity, err := f.verify(f.signRS256(t, claims))
			if tc.wantErr {
				if err == nil {
					t.Fatal("Verify() error = nil, want a malformed permissions claim to be rejected")
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}
			if len(identity.Permissions) != len(tc.want) {
				t.Fatalf("Permissions = %v, want %v", identity.Permissions, tc.want)
			}
			for i, want := range tc.want {
				if identity.Permissions[i] != want {
					t.Fatalf("Permissions[%d] = %q, want %q", i, identity.Permissions[i], want)
				}
			}
		})
	}
}

func TestVerifierErrorsAreClassifiedAsInvalidToken(t *testing.T) {
	f := newAuthFixture(t)

	_, err := f.verify("not-a-token")
	if err == nil {
		t.Fatal("Verify() error = nil, want an error")
	}
	if !errors.Is(err, domain.ErrAdminTokenInvalid) {
		t.Fatalf("Verify() error = %v, want it to wrap domain.ErrAdminTokenInvalid", err)
	}
}

// ---------------------------------------------------------------------------
// Fail-closed behavior and construction
// ---------------------------------------------------------------------------

func TestVerifierFailsClosedWhenKeysUnavailable(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK(testKid, &key.PublicKey)))
	endpoint.setResponse(nil, http.StatusInternalServerError)
	clock := newTestClock()

	cfg, err := NewConfig(testDomain, testAudience)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	verifier, err := NewVerifier(cfg,
		WithHTTPClient(endpoint.client()),
		WithClock(clock.Now),
		WithJWKSURL(endpoint.url()),
	)
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	token := signWithKey(t, key, testKid, jwt.MapClaims{
		"iss": cfg.Issuer(),
		"aud": testAudience,
		"exp": clock.Now().Add(time.Hour).Unix(),
	}, jwt.SigningMethodRS256)

	if _, err := verifier.Verify(context.Background(), token); err == nil {
		t.Fatal("Verify() error = nil, want an unavailable key to fail closed")
	}
}

// Constructing a verifier must never perform network I/O: key retrieval is lazy
// and happens only when a credential needs it.
func TestNewVerifierDoesNotFetchDuringConstruction(t *testing.T) {
	key := generateRSAKey(t)
	endpoint := newJWKSEndpoint(t, buildJWKS(t, rsaJWK(testKid, &key.PublicKey)))

	cfg, err := NewConfig(testDomain, testAudience)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if _, err := NewVerifier(cfg, WithHTTPClient(endpoint.client()), WithJWKSURL(endpoint.url())); err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	if got := endpoint.count(); got != 0 {
		t.Fatalf("JWKS fetches during construction = %d, want 0", got)
	}
}

func TestNewVerifierRejectsInvalidConfig(t *testing.T) {
	if _, err := NewVerifier(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewVerifier(zero config) error = %v, want it to wrap ErrInvalidConfig", err)
	}
	if _, err := NewVerifier(Config{Domain: "tenant.us.auth0.com"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewVerifier(missing audience) error = %v, want it to wrap ErrInvalidConfig", err)
	}
}

// Options must let a caller tune the cache without changing the trust boundary.
func TestVerifierOptionsOverrideCachePolicy(t *testing.T) {
	f := newAuthFixture(t)
	if f.verifier.keys.jwksURL != f.endpoint.url() {
		t.Fatalf("jwksURL = %q, want the injected endpoint %q", f.verifier.keys.jwksURL, f.endpoint.url())
	}
	if f.verifier.keys.ttl != defaultCacheTTL {
		t.Errorf("ttl = %v, want the default %v", f.verifier.keys.ttl, defaultCacheTTL)
	}
	if f.verifier.now == nil {
		t.Error("now = nil, want the injected clock")
	}
	if got, want := f.verifier.config.Issuer(), f.config.Issuer(); got != want {
		t.Errorf("Issuer() = %q, want %q", got, want)
	}
}
