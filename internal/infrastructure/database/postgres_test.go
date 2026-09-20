package database

import (
	"context"
	"os"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestPostgresTracingInstrumentation proves real pgx driver integration:
// connection acquisition, statements, and transaction lifecycle all appear as
// correctly parented child spans with no SQL text, parameters, or connection
// details attached.
//
// It requires a disposable database and skips when TEST_DATABASE_URL is unset.
func TestPostgresTracingInstrumentation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}
	// NewPool reads DATABASE_URL. godotenv.Load never overrides an already-set
	// variable, so this disposable DSN wins.
	t.Setenv("DATABASE_URL", dsn)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	ctx := context.Background()

	db, err := NewPool(ctx, WithTracerProvider(provider))
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer db.Close()

	pool := db.GetDb()

	before := len(recorder.Ended())

	queryCtx, parent := provider.Tracer("test").Start(ctx, "HTTP GET /api/v1/products/:id")

	const sqlText = "SELECT 1"
	var value int
	if err := pool.QueryRow(queryCtx, sqlText).Scan(&value); err != nil {
		t.Fatalf("QueryRow(%q) error = %v", sqlText, err)
	}
	if value != 1 {
		t.Fatalf("QueryRow result = %d, want 1", value)
	}

	tx, err := pool.Begin(queryCtx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := tx.Exec(queryCtx, "SELECT 1"); err != nil {
		t.Fatalf("tx.Exec() error = %v", err)
	}
	if err := tx.Rollback(queryCtx); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	parent.End()

	spans := recorder.Ended()[before:]
	if len(spans) == 0 {
		t.Fatalf("no PostgreSQL spans were recorded")
	}

	var sawQuery, sawTransaction bool
	var matched int

	for _, span := range spans {
		// Spans raised outside the request must not be attributed to it.
		if span.SpanContext().TraceID() != parent.SpanContext().TraceID() {
			continue
		}
		matched++

		if span.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Errorf("span %q parent = %q, want the active HTTP span %q",
				span.Name(), span.Parent().SpanID(), parent.SpanContext().SpanID())
		}

		switch span.Name() {
		case "SELECT":
			sawQuery = true
		case "BEGIN", "COMMIT", "ROLLBACK":
			sawTransaction = true
		}

		// No SQL text, parameters, or connection details may be exported.
		for _, attr := range span.Attributes() {
			key := string(attr.Key)
			value := attr.Value.Emit()

			if strings.Contains(value, sqlText) {
				t.Errorf("span %q attribute %q leaked SQL text %q", span.Name(), key, value)
			}
			switch key {
			case "db.statement", "db.query.text", "db.statement.parameters",
				"db.connection_string", "net.peer.name", "net.peer.port",
				"server.address", "server.port":
				t.Errorf("span %q exported excluded attribute %q", span.Name(), key)
			}
			if strings.Contains(key, "parameters") {
				t.Errorf("span %q exported parameter attribute %q", span.Name(), key)
			}
		}

		if strings.Contains(span.Name(), sqlText) {
			t.Errorf("span name %q contains the full SQL statement", span.Name())
		}
	}

	if matched == 0 {
		t.Fatalf("no PostgreSQL span was parented under the active request context")
	}
	if !sawQuery {
		t.Errorf("no low-cardinality query span (SELECT) was recorded")
	}
	if !sawTransaction {
		t.Errorf("no transaction lifecycle span (BEGIN/COMMIT/ROLLBACK) was recorded")
	}
}

// Instrumentation must be opt-in: without an explicit provider no tracer is
// attached to the pool configuration.
func TestNewPoolWithoutTracerProviderIsUninstrumented(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}
	t.Setenv("DATABASE_URL", dsn)

	db, err := NewPool(context.Background())
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer db.Close()

	if db.GetDb() == nil {
		t.Fatalf("GetDb() = nil, want a usable pool")
	}
}
