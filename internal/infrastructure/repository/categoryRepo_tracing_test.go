package repository

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCategoryFindAllRepositoryErrorIsTraced(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://trace:trace@127.0.0.1:1/trace?connect_timeout=1")
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	ctx, parent := provider.Tracer("test").Start(context.Background(), "usecase.category.find_all")
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	repo := NewCategoryRepo(&categoryTracingDatabase{pool: pool})
	_, _, err = repo.FindAll(ctx, domain.ListCategoryParams{Page: 1, PerPage: 10})
	parent.End()
	if err == nil {
		t.Fatal("FindAll() error = nil, want canceled repository error")
	}

	span := findCategoryRepositorySpan(t, recorder.Ended(), "repository.category.find_all")
	if span.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("repository parent = %q, want use-case span %q", span.Parent().SpanID(), parent.SpanContext().SpanID())
	}
	if span.Status().Code != codes.Error {
		t.Errorf("repository status = %v, want error", span.Status().Code)
	}
	if !categoryRepositoryHasException(span) {
		t.Error("repository span must record an exception event")
	}
}

func findCategoryRepositorySpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("span %q not found", name)
	return nil
}

func categoryRepositoryHasException(span sdktrace.ReadOnlySpan) bool {
	for _, event := range span.Events() {
		if event.Name == "exception" {
			return true
		}
	}
	return false
}

type categoryTracingDatabase struct {
	pool *pgxpool.Pool
}

func (d *categoryTracingDatabase) GetDb() *pgxpool.Pool { return d.pool }
func (d *categoryTracingDatabase) Close()               { d.pool.Close() }
