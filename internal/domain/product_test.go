package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewProduct(t *testing.T) {
	p, err := NewProduct("my-handle", "My Title", 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if p.Status != ProductStatusDraft {
		t.Errorf("expected draft status, got %q", p.Status)
	}
	if p.ID == uuid.Nil {
		t.Error("expected generated ID")
	}
	if p.Handle != "my-handle" || p.Title != "My Title" {
		t.Errorf("expected handle/title preserved, got %q/%q", p.Handle, p.Title)
	}
	if p.CategoryID != 1 {
		t.Errorf("expected category id 1, got %d", p.CategoryID)
	}

	if _, err := NewProduct("", "My Title", 1); err != ErrProductInvalidInput {
		t.Fatalf("expected ErrProductInvalidInput for empty handle, got %v", err)
	}
	if _, err := NewProduct("my-handle", "My Title", 0); err != ErrProductInvalidInput {
		t.Fatalf("expected ErrProductInvalidInput for invalid category, got %v", err)
	}
}

func TestProductAddVariant(t *testing.T) {
	p, _ := NewProduct("h", "t", 1)
	sku := "SKU-1"

	if err := p.AddVariant(Variant{SKU: &sku}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(p.Variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(p.Variants))
	}

	if err := p.AddVariant(Variant{SKU: &sku}); err != ErrSKUAlreadyExists {
		t.Fatalf("expected ErrSKUAlreadyExists, got %v", err)
	}
	if len(p.Variants) != 1 {
		t.Fatalf("expected rejected variant not appended, got %d", len(p.Variants))
	}

	// nil/empty SKUs never collide with each other
	if err := p.AddVariant(Variant{}); err != nil {
		t.Fatalf("expected no error for nil SKU, got %v", err)
	}
	if err := p.AddVariant(Variant{}); err != nil {
		t.Fatalf("expected no error for second nil SKU, got %v", err)
	}
}

func TestProductStatusCanTransitionTo(t *testing.T) {
	cases := []struct {
		from, to ProductStatus
		want     bool
	}{
		{ProductStatusDraft, ProductStatusActive, true},
		{ProductStatusDraft, ProductStatusArchived, true},
		{ProductStatusActive, ProductStatusArchived, true},
		{ProductStatusArchived, ProductStatusActive, true},
		{ProductStatusActive, ProductStatusDraft, false},
		{ProductStatusArchived, ProductStatusDraft, false},
		{ProductStatusDraft, ProductStatusDraft, false},
	}
	for _, c := range cases {
		if got := c.from.CanTransitionTo(c.to); got != c.want {
			t.Errorf("%s -> %s: got %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestProductArchiveRestore(t *testing.T) {
	p, _ := NewProduct("h", "t", 1)

	if err := p.Archive(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if p.Status != ProductStatusArchived {
		t.Errorf("expected archived status, got %q", p.Status)
	}

	if err := p.Archive(); err != ErrProductInvalidStatusTransition {
		t.Fatalf("expected ErrProductInvalidStatusTransition, got %v", err)
	}

	if err := p.Restore(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if p.Status != ProductStatusActive {
		t.Errorf("expected active status, got %q", p.Status)
	}

	if err := p.Restore(); err != ErrProductInvalidStatusTransition {
		t.Fatalf("expected ErrProductInvalidStatusTransition, got %v", err)
	}
}
