package infrachecker

import (
	"context"
	"errors"
	"testing"

	"gin-product-service/internal/domain"
)

type fakeInfraRepo struct {
	called bool
	err    error
}

func (f *fakeInfraRepo) PingDb(ctx context.Context) error {
	f.called = true
	return f.err
}

func TestNewInfraCheckerUseCase_CheckDatabase(t *testing.T) {
	repo := &fakeInfraRepo{}
	uc := NewInfraCheckerUseCase(repo)

	if err := uc.CheckDatabase(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !repo.called {
		t.Fatal("expected repository PingDb to be called")
	}
}

func TestNewInfraCheckerUseCase_CheckDatabase_PropagatesError(t *testing.T) {
	expected := errors.New("db unavailable")
	repo := &fakeInfraRepo{err: expected}
	uc := NewInfraCheckerUseCase(repo)

	if err := uc.CheckDatabase(context.Background()); !errors.Is(err, expected) {
		t.Fatalf("expected error %v, got %v", expected, err)
	}
}

var _ domain.InfraCheckUseCase = (*infraCheckerUseCase)(nil)
