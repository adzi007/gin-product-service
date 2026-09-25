package auth0

import (
	"errors"
	"strings"
	"testing"
)

// The Auth0 domain is a bare host name. The issuer and the JWKS URL are derived
// from it alone, so a token can never redirect key retrieval.
func TestNewConfigDerivesTrustedIssuerAndJWKSURL(t *testing.T) {
	cfg, err := NewConfig("tenant.us.auth0.com", "https://api.example.com/")
	if err != nil {
		t.Fatalf("NewConfig() error = %v, want nil", err)
	}

	if cfg.Domain != "tenant.us.auth0.com" {
		t.Errorf("Domain = %q, want %q", cfg.Domain, "tenant.us.auth0.com")
	}
	if cfg.Audience != "https://api.example.com/" {
		t.Errorf("Audience = %q, want %q", cfg.Audience, "https://api.example.com/")
	}

	if got, want := cfg.Issuer(), "https://tenant.us.auth0.com/"; got != want {
		t.Errorf("Issuer() = %q, want %q", got, want)
	}
	if got, want := cfg.JWKSURL(), "https://tenant.us.auth0.com/.well-known/jwks.json"; got != want {
		t.Errorf("JWKSURL() = %q, want %q", got, want)
	}
}

// Surrounding whitespace from environment values is tolerated; the derived URLs
// stay exact.
func TestNewConfigTrimsSurroundingWhitespace(t *testing.T) {
	cfg, err := NewConfig("  tenant.us.auth0.com  ", " https://api.example.com/ ")
	if err != nil {
		t.Fatalf("NewConfig() error = %v, want nil", err)
	}
	if cfg.Domain != "tenant.us.auth0.com" {
		t.Errorf("Domain = %q, want the trimmed host", cfg.Domain)
	}
	if cfg.Audience != "https://api.example.com/" {
		t.Errorf("Audience = %q, want the trimmed audience", cfg.Audience)
	}
	if got, want := cfg.Issuer(), "https://tenant.us.auth0.com/"; got != want {
		t.Errorf("Issuer() = %q, want %q", got, want)
	}
}

// A scheme, path, query, fragment, or userinfo in the domain is rejected rather
// than silently rewritten, so a misconfigured deployment cannot derive a
// surprising fetch target.
func TestNewConfigRejectsMalformedDomainAndAudience(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		audience string
	}{
		{name: "missing domain", domain: "", audience: "https://api.example.com/"},
		{name: "blank domain", domain: "   ", audience: "https://api.example.com/"},
		{name: "domain with scheme", domain: "https://tenant.us.auth0.com", audience: "https://api.example.com/"},
		{name: "domain with insecure scheme", domain: "http://tenant.us.auth0.com", audience: "https://api.example.com/"},
		{name: "domain with trailing slash", domain: "tenant.us.auth0.com/", audience: "https://api.example.com/"},
		{name: "domain with path", domain: "tenant.us.auth0.com/oauth/token", audience: "https://api.example.com/"},
		{name: "domain with userinfo", domain: "user@tenant.us.auth0.com", audience: "https://api.example.com/"},
		{name: "domain with query", domain: "tenant.us.auth0.com?x=1", audience: "https://api.example.com/"},
		{name: "domain with fragment", domain: "tenant.us.auth0.com#frag", audience: "https://api.example.com/"},
		{name: "domain with inner space", domain: "tenant us.auth0.com", audience: "https://api.example.com/"},
		{name: "missing audience", domain: "tenant.us.auth0.com", audience: ""},
		{name: "blank audience", domain: "tenant.us.auth0.com", audience: "  "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewConfig(tc.domain, tc.audience)
			if err == nil {
				t.Fatalf("NewConfig(%q, %q) error = nil, want an error", tc.domain, tc.audience)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewConfig(%q, %q) error = %v, want it to wrap ErrInvalidConfig", tc.domain, tc.audience, err)
			}
			if cfg.Domain != "" || cfg.Audience != "" {
				t.Fatalf("NewConfig(%q, %q) returned a partially populated config %+v", tc.domain, tc.audience, cfg)
			}
		})
	}
}

// A validated config never reuses the customer HS256 secret: the admin trust
// boundary is asymmetric and derived only from the Auth0 domain.
func TestConfigNeverCarriesSecretMaterial(t *testing.T) {
	cfg, err := NewConfig("tenant.us.auth0.com", "https://api.example.com/")
	if err != nil {
		t.Fatalf("NewConfig() error = %v, want nil", err)
	}

	combined := cfg.Issuer() + " " + cfg.JWKSURL()
	for _, forbidden := range []string{"API_JWT_SECRET", "HS256", "secret", "token"} {
		if strings.Contains(strings.ToLower(combined), strings.ToLower(forbidden)) {
			t.Errorf("derived configuration %q unexpectedly contains %q", combined, forbidden)
		}
	}
}

// A zero-value Config is not usable, which is why the verifier re-validates it.
func TestConfigValidateRejectsZeroValue(t *testing.T) {
	if err := (Config{}).validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() on zero value = %v, want it to wrap ErrInvalidConfig", err)
	}
}
