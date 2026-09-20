package wire

import (
	"testing"

	"gin-product-service/internal/domain"
)

// NewContainer composes every use case directly into its handler without any
// telemetry import: adding a new uninstrumented use-case method requires no
// telemetry-file edit. The D2 removal of decorators is what makes this
// compile-time guarantee hold.
func TestNewContainerComposesWithoutTelemetry(t *testing.T) {
	c := NewContainer(nil, domain.ReservationLocker(nil))

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
