package wire

import (
	"errors"
	"testing"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/auth0"
)

// validAdminConfig returns an Auth0 configuration the composition root accepts.
func validAdminConfig(t *testing.T) auth0.Config {
	t.Helper()

	config, err := auth0.NewConfig("tenant.us.auth0.com", "https://api.example.com/")
	if err != nil {
		t.Fatalf("auth0.NewConfig() error = %v", err)
	}
	return config
}

// NewContainer composes every use case directly into its handler without any
// telemetry import: adding a new uninstrumented use-case method requires no
// telemetry-file edit. The D2 removal of decorators is what makes this
// compile-time guarantee hold.
func TestNewContainerComposesWithoutTelemetry(t *testing.T) {
	c, err := NewContainer(nil, domain.ReservationLocker(nil), validAdminConfig(t))
	if err != nil {
		t.Fatalf("NewContainer() error = %v, want nil", err)
	}

	if c.CategoryHandler == nil {
		t.Fatalf("CategoryHandler = nil")
	}
	if c.ProductHandler == nil {
		t.Fatalf("ProductHandler = nil")
	}
	if c.ReviewHandler == nil {
		t.Fatalf("ReviewHandler = nil")
	}
	if c.InventoryHandler == nil {
		t.Fatalf("InventoryHandler = nil")
	}
	if c.InfraCheckerUseCase == nil {
		t.Fatalf("InfraCheckerUseCase = nil")
	}
}

// The container owns exactly one admin verifier built from validated
// configuration and exposes it through the domain port for router injection.
func TestNewContainerOwnsAdminTokenVerifier(t *testing.T) {
	c, err := NewContainer(nil, domain.ReservationLocker(nil), validAdminConfig(t))
	if err != nil {
		t.Fatalf("NewContainer() error = %v, want nil", err)
	}

	if c.AdminTokenVerifier == nil {
		t.Fatal("AdminTokenVerifier = nil, want the container to own one verifier")
	}

	// It is usable as the domain port and fails closed without key material: a
	// credential cannot be verified before the JWKS cache is populated.
	identity, err := c.AdminTokenVerifier.Verify(t.Context(), "not-a-token")
	if err == nil {
		t.Fatal("Verify(invalid) error = nil, want a fail-closed rejection")
	}
	if !errors.Is(err, domain.ErrAdminTokenInvalid) {
		t.Fatalf("Verify(invalid) error = %v, want domain.ErrAdminTokenInvalid", err)
	}
	if identity.Subject != "" || identity.Permissions != nil {
		t.Fatalf("Verify(invalid) identity = %+v, want the zero identity", identity)
	}
}

// An unusable configuration is rejected instead of producing a permissive
// verifier.
func TestNewContainerRejectsInvalidAdminConfig(t *testing.T) {
	c, err := NewContainer(nil, domain.ReservationLocker(nil), auth0.Config{})
	if err == nil {
		t.Fatal("NewContainer(zero config) error = nil, want a configuration error")
	}
	if c != nil {
		t.Fatalf("NewContainer(zero config) returned %+v, want nil", c)
	}
}
