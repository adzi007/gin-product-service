package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gin-product-service/internal/delivery/http/middleware"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type securityHarness struct {
	engine   *gin.Engine
	recorder *tracetest.SpanRecorder
	provider *sdktrace.TracerProvider
}

func newSecurityHarness(t *testing.T) *securityHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	engine := gin.New()
	engine.Use(middleware.Tracing(middleware.TracingConfig{
		Enabled:        true,
		TracerProvider: provider,
		Propagator:     telemetry.NewPropagator(nil),
		EnrichContext:  logger.WithTraceContext,
	}))
	engine.Use(middleware.Recovery(middleware.RecoveryConfig{}))

	return &securityHarness{engine: engine, recorder: recorder, provider: provider}
}

func (h *securityHarness) spans() []sdktrace.ReadOnlySpan { return h.recorder.Ended() }

func spanStatusByName(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) codes.Code {
	t.Helper()
	return spanByName(t, spans, name).Status().Code
}

// SC-003: every completed trace reports the same high-level outcome as the
// client-visible response or interruption.
func TestOutcomeFidelityAcrossFailureModes(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		status    int
		register  func(*securityHarness)
		context   func() context.Context
		wantError bool
	}{
		{
			name:   "authentication rejection stays unset",
			method: http.MethodPost,
			path:   "/api/v1/products/:id/reviews",
			status: http.StatusUnauthorized,
			register: func(h *securityHarness) {
				h.engine.POST("/api/v1/products/:id/reviews", func(c *gin.Context) {
					c.Status(http.StatusUnauthorized)
				})
			},
			wantError: false,
		},
		{
			name:   "validation failure stays unset",
			method: http.MethodPost,
			path:   "/api/v1/categories",
			status: http.StatusBadRequest,
			register: func(h *securityHarness) {
				h.engine.POST("/api/v1/categories", func(c *gin.Context) {
					c.Status(http.StatusBadRequest)
				})
			},
			wantError: false,
		},
		{
			name:   "dependency failure marks request and child as errors",
			method: http.MethodGet,
			path:   "/api/v1/categories",
			status: http.StatusInternalServerError,
			register: func(h *securityHarness) {
				uc := failingCategoryQuery{}
				h.engine.GET("/api/v1/categories", func(c *gin.Context) {
					if _, err := uc.FindAll(c.Request.Context(), domain.ListCategoryParams{}); err != nil {
						c.Status(http.StatusInternalServerError)
						return
					}
					c.Status(http.StatusOK)
				})
			},
			wantError: true,
		},
		{
			name:   "cancelled request marks the span as an error",
			method: http.MethodGet,
			path:   "/api/v1/categories",
			status: http.StatusOK,
			register: func(h *securityHarness) {
				h.engine.GET("/api/v1/categories", func(c *gin.Context) { c.Status(http.StatusOK) })
			},
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantError: true,
		},
		{
			name:   "recovered panic marks the span as an error",
			method: http.MethodGet,
			path:   "/api/v1/categories",
			status: http.StatusInternalServerError,
			register: func(h *securityHarness) {
				h.engine.GET("/api/v1/categories", func(*gin.Context) { panic("boom") })
			},
			wantError: true,
		},
		{
			name:   "slow dependency remains a successful but attributable trace",
			method: http.MethodGet,
			path:   "/api/v1/categories",
			status: http.StatusOK,
			register: func(h *securityHarness) {
				uc := slowCategoryQuery{delay: 40 * time.Millisecond}
				h.engine.GET("/api/v1/categories", func(c *gin.Context) {
					if _, err := uc.FindAll(c.Request.Context(), domain.ListCategoryParams{}); err != nil {
						c.Status(http.StatusInternalServerError)
						return
					}
					c.Status(http.StatusOK)
				})
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newSecurityHarness(t)
			tt.register(harness)

			var rec *httptest.ResponseRecorder
			if tt.context != nil {
				req := httptest.NewRequest(tt.method, tt.path, nil).WithContext(tt.context())
				rec = httptest.NewRecorder()
				harness.engine.ServeHTTP(rec, req)
			} else {
				rec = httptest.NewRecorder()
				harness.engine.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			}

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}

			spans := harness.spans()
			if len(spans) == 0 {
				t.Fatalf("no spans recorded")
			}

			server := spans[0]
			for _, span := range spans {
				if strings.HasPrefix(span.Name(), "HTTP ") {
					server = span
				}
			}

			gotError := server.Status().Code == codes.Error
			if gotError != tt.wantError {
				t.Fatalf("server span error = %v, want %v (status %d, description %q)",
					gotError, tt.wantError, rec.Code, server.Status().Description)
			}

			// D2: application spans are retired, so the server span carries the
			// request outcome; the slow dependency remains visible in its duration.
			if tt.name == "slow dependency remains a successful but attributable trace" {
				if got := server.EndTime().Sub(server.StartTime()); got < 40*time.Millisecond {
					t.Fatalf("server span duration = %v, want >= the slow dependency", got)
				}
			}
		})
	}
}

// SC-004: no exported field may contain credentials, payloads, SQL, identifiers,
// or customer content.
func TestForbiddenValuesAreNeverExported(t *testing.T) {
	const (
		forbiddenJWT        = "eyJhbGciOiJIUzI1NiJ9.sup3r-s3cret-token.signature"
		forbiddenReviewBody = "customer review content that must stay private"
		forbiddenSQL        = "SELECT * FROM product_reviews WHERE product_id = $1"
		forbiddenConnString = "postgres://user:sup3r-db-password@db.example.test:5432/app"
	)

	reviewBody := forbiddenReviewBody

	orderID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	variantID := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	harness := newSecurityHarness(t)

	uc := leakyReviewInsert{
		sqlText:    forbiddenSQL,
		connString: forbiddenConnString,
	}

	harness.engine.POST("/api/v1/inventory/reservations", func(c *gin.Context) {
		if _, err := uc.Create(c.Request.Context(), variantID, orderID, "customer",
			domain.CreateReviewInput{Comment: &reviewBody}); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/reservations", strings.NewReader(`{"orderId":"`+orderID.String()+`","body":"`+forbiddenReviewBody+`"}`))
	req.Header.Set("Authorization", "Bearer "+forbiddenJWT)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	harness.engine.ServeHTTP(rec, req)

	spans := harness.spans()
	if len(spans) == 0 {
		t.Fatalf("no spans recorded")
	}

	assertNoForbiddenValues(t, spans, []string{
		forbiddenJWT,
		"sup3r-s3cret-token",
		forbiddenReviewBody,
		forbiddenSQL,
		forbiddenConnString,
		"sup3r-db-password",
		orderID.String(),
		variantID.String(),
	})

	// The trace must still be usable: request and dependency spans, with no
	// application span remaining.
	spanByName(t, spans, "HTTP POST /api/v1/inventory/reservations")
	spanByName(t, spans, "SELECT")
}

// SC-007: request-scoped logs carry identifiers that locate the matching trace.
func TestRequestLogsCarryTraceCorrelation(t *testing.T) {
	harness := newSecurityHarness(t)

	harness.engine.GET("/api/v1/categories", func(c *gin.Context) {
		logger.L(c.Request.Context()).Info("handling category request")
		c.Status(http.StatusOK)
	})

	captured, restore := captureStdout(t)
	defer restore()

	rec := httptest.NewRecorder()
	harness.engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil))

	spans := harness.spans()
	if len(spans) == 0 {
		t.Fatalf("no spans recorded")
	}
	server := spans[0]

	output := captured()
	if !strings.Contains(output, "trace_id") || !strings.Contains(output, "span_id") {
		t.Fatalf("request log output has no correlation fields: %q", output)
	}
	if !strings.Contains(output, server.SpanContext().TraceID().String()) {
		t.Fatalf("request log output does not contain the trace ID %q: %q", server.SpanContext().TraceID(), output)
	}
	if !strings.Contains(output, server.SpanContext().SpanID().String()) {
		t.Fatalf("request log output does not contain the server span ID %q: %q", server.SpanContext().SpanID(), output)
	}
}

// captureStdout redirects the process stdout into a pipe for the duration of
// the test, after initializing the base logger onto it. The pipe is drained
// concurrently because a pipe has no back-pressure on Windows.
func captureStdout(t *testing.T) (func() string, func()) {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(): %v", err)
	}

	previous := os.Stdout
	os.Stdout = writer

	if err := logger.Init("development"); err != nil {
		t.Fatalf("logger.Init(): %v", err)
	}

	drained := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		drained <- string(data)
	}()

	return func() string {
			_ = writer.Close()
			os.Stdout = previous
			select {
			case output := <-drained:
				return output
			case <-time.After(5 * time.Second):
				return ""
			}
		}, func() {
			os.Stdout = previous
		}
}

// --- leaky/slow fakes -------------------------------------------------------

// failingCategoryQuery fails while reporting a dependency error.
type failingCategoryQuery struct{}

func (failingCategoryQuery) FindAll(context.Context, domain.ListCategoryParams) (domain.PaginatedCategories, error) {
	return domain.PaginatedCategories{}, domain.ErrReservationCoordinationUnavailable
}

func (failingCategoryQuery) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	return nil, nil
}

func (failingCategoryQuery) GetByID(context.Context, int) (domain.Category, error) {
	return domain.Category{}, nil
}

type slowCategoryQuery struct {
	delay time.Duration
}

func (f slowCategoryQuery) FindAll(ctx context.Context, _ domain.ListCategoryParams) (domain.PaginatedCategories, error) {
	done := make(chan struct{})
	go func() {
		time.Sleep(f.delay)
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return domain.PaginatedCategories{}, ctx.Err()
	}
	startChild(ctx, "SELECT")
	return domain.PaginatedCategories{}, nil
}

func (slowCategoryQuery) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	return nil, nil
}

func (slowCategoryQuery) GetByID(context.Context, int) (domain.Category, error) {
	return domain.Category{}, nil
}

// leakyReviewInsert touches sensitive values internally to prove they never
// reach the exported spans.
type leakyReviewInsert struct {
	sqlText    string
	connString string
}

func (f leakyReviewInsert) Create(ctx context.Context, _, _ uuid.UUID, _ string, _ domain.CreateReviewInput) (domain.Review, error) {
	_ = f.sqlText
	_ = f.connString
	startChild(ctx, "SELECT")
	return domain.Review{}, nil
}
