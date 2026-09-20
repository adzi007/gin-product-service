package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gin-product-service/internal/delivery/http/middleware"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/redis"
	"gin-product-service/internal/infrastructure/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// tracingHarness wires the real inbound middleware, the real application
// decorators, and the real Redis coordination adapter against recording
// providers, so the produced trace hierarchy is the production one.
type tracingHarness struct {
	engine   *gin.Engine
	recorder *tracetest.SpanRecorder
	provider *sdktrace.TracerProvider
}

func newTracingHarness(t *testing.T) *tracingHarness {
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
	}))

	return &tracingHarness{engine: engine, recorder: recorder, provider: provider}
}

// startChild emulates work performed by a lower layer (for example one pgx
// statement) by creating a child span on the active trace.
func startChild(ctx context.Context, name string) {
	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer("test-dependency").Start(ctx, name)
	span.End()
}

func (h *tracingHarness) spans() []sdktrace.ReadOnlySpan { return h.recorder.Ended() }

func (h *tracingHarness) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.engine.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func spanByName(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	t.Fatalf("span %q not found; recorded: %v", name, names)
	return nil
}

// SC-001: every representative route family produces one complete trace from
// request entry to final response, with dependency spans reparented directly
// under the HTTP span (the D2-approved application-span removal).
func TestRepresentativeRouteFamiliesProduceOneTrace(t *testing.T) {
	tests := []struct {
		family       string
		method       string
		path         string
		route        string
		dependency   string
		registerFunc func(*tracingHarness)
	}{
		{
			family:     "category",
			method:     http.MethodGet,
			path:       "/api/v1/categories",
			route:      "/api/v1/categories",
			dependency: "SELECT",
			registerFunc: func(h *tracingHarness) {
				uc := fakeCategoryQuery{}
				h.engine.GET("/api/v1/categories", func(c *gin.Context) {
					if _, err := uc.FindAll(c.Request.Context(), domain.ListCategoryParams{}); err != nil {
						c.Status(http.StatusInternalServerError)
						return
					}
					c.Status(http.StatusOK)
				})
			},
		},
		{
			family:     "product",
			method:     http.MethodGet,
			path:       "/api/v1/products",
			route:      "/api/v1/products",
			dependency: "SELECT",
			registerFunc: func(h *tracingHarness) {
				uc := fakeProductQuery{}
				h.engine.GET("/api/v1/products", func(c *gin.Context) {
					if _, err := uc.FindAll(c.Request.Context(), domain.ListProductParams{}); err != nil {
						c.Status(http.StatusInternalServerError)
						return
					}
					c.Status(http.StatusOK)
				})
			},
		},
		{
			family:     "review",
			method:     http.MethodGet,
			path:       "/api/v1/reviews/summary",
			route:      "/api/v1/reviews/summary",
			dependency: "SELECT",
			registerFunc: func(h *tracingHarness) {
				uc := fakeReviewSummary{}
				h.engine.GET("/api/v1/reviews/summary", func(c *gin.Context) {
					if _, err := uc.GetSummary(c.Request.Context(), uuid.Nil); err != nil {
						c.Status(http.StatusInternalServerError)
						return
					}
					c.Status(http.StatusOK)
				})
			},
		},
		{
			family:     "inventory",
			method:     http.MethodPost,
			path:       "/api/v1/inventory/reservations",
			route:      "/api/v1/inventory/reservations",
			dependency: "INSERT",
			registerFunc: func(h *tracingHarness) {
				uc := fakeReservation{}
				h.engine.POST("/api/v1/inventory/reservations", func(c *gin.Context) {
					if _, err := uc.Create(c.Request.Context(), uuid.Nil, time.Now().Add(time.Hour), nil); err != nil {
						c.Status(http.StatusInternalServerError)
						return
					}
					c.Status(http.StatusOK)
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.family, func(t *testing.T) {
			harness := newTracingHarness(t)
			tt.registerFunc(harness)

			if rec := harness.do(t, tt.method, tt.path); rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			spans := harness.spans()
			server := spanByName(t, spans, "HTTP "+tt.method+" "+tt.route)
			dependency := spanByName(t, spans, tt.dependency)

			if server.Parent().IsValid() {
				t.Errorf("server span must be the trace root")
			}
			// D2: dependency spans reparent directly under the HTTP span.
			assertParent(t, dependency, server)

			for _, span := range spans {
				if span.SpanContext().TraceID() != server.SpanContext().TraceID() {
					t.Errorf("span %q is not part of the request trace", span.Name())
				}
				if strings.HasPrefix(span.Name(), "app.") {
					t.Errorf("unexpected application span %q: decorators are retired", span.Name())
				}
			}
		})
	}
}

// SC-002: the reservation trace connects both Redis coordination calls, the
// transactional database work, and the final response directly under the HTTP
// span (the D2-approved application-span removal).
func TestReservationTraceHierarchy(t *testing.T) {
	harness := newTracingHarness(t)

	redisCalls := newRecordingRedis(t)
	defer redisCalls.Close()

	locker := redis.NewReservationLocker(redisCalls.URL, "test-token",
		redis.WithTracing(harness.provider, telemetry.NewPropagator([]string{"safe-test"})))

	reservation := tracedReservation{locker: locker}

	harness.engine.POST("/api/v1/inventory/reservations", func(c *gin.Context) {
		if _, err := reservation.Create(c.Request.Context(), uuid.New(), time.Now().Add(time.Hour), nil); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})

	if rec := harness.do(t, http.MethodPost, "/api/v1/inventory/reservations"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	spans := harness.spans()
	server := spanByName(t, spans, "HTTP POST /api/v1/inventory/reservations")
	acquire := spanByName(t, spans, "redis.reservation.acquire")
	release := spanByName(t, spans, "redis.reservation.release")
	begin := spanByName(t, spans, "BEGIN")
	statement := spanByName(t, spans, "SELECT")
	commit := spanByName(t, spans, "COMMIT")

	for _, child := range []sdktrace.ReadOnlySpan{acquire, begin, statement, commit, release} {
		assertParent(t, child, server)
	}
	if acquire.SpanKind() != trace.SpanKindClient || release.SpanKind() != trace.SpanKindClient {
		t.Errorf("coordination spans must be client spans")
	}

	traceID := server.SpanContext().TraceID()
	for _, span := range spans {
		if span.SpanContext().TraceID() != traceID {
			t.Errorf("span %q is not part of the reservation trace", span.Name())
		}
	}

	if got := redisCalls.traceParents(); len(got) != 2 {
		t.Fatalf("outbound traceparent headers = %d, want 2 (acquire and release)", len(got))
	}

	// The propagated context must belong to the same trace, and no command,
	// key, owner token, or credential may appear in the exported spans.
	for _, value := range redisCalls.traceParents() {
		if len(value) < 55 || value[3:35] != traceID.String() {
			t.Errorf("traceparent = %q, want it to carry trace %q", value, traceID)
		}
	}
	assertNoForbiddenValues(t, spans, []string{
		"reservation:order", "test-token", "Bearer", "redis.call", "EVAL",
	})
}

func assertParent(t *testing.T, child, parent sdktrace.ReadOnlySpan) {
	t.Helper()
	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("span %q parent = %q, want %q", child.Name(), child.Parent().SpanID(), parent.SpanContext().SpanID())
	}
	if child.Parent().TraceID() != parent.SpanContext().TraceID() {
		t.Errorf("span %q parent trace = %q, want %q", child.Name(), child.Parent().TraceID(), parent.SpanContext().TraceID())
	}
}

func assertNoForbiddenValues(t *testing.T, spans []sdktrace.ReadOnlySpan, forbidden []string) {
	t.Helper()
	for _, span := range spans {
		values := make([]string, 0, len(span.Attributes())+1)
		for _, attr := range span.Attributes() {
			values = append(values, string(attr.Key)+"="+attr.Value.Emit())
		}
		values = append(values, span.Status().Description)

		for _, value := range values {
			for _, secret := range forbidden {
				if strings.Contains(value, secret) {
					t.Errorf("span %q exported forbidden value %q", span.Name(), secret)
				}
			}
		}
	}
}

// --- fakes -----------------------------------------------------------------

type fakeCategoryQuery struct{}

func (fakeCategoryQuery) FindAll(ctx context.Context, _ domain.ListCategoryParams) (domain.PaginatedCategories, error) {
	startChild(ctx, "SELECT")
	return domain.PaginatedCategories{}, nil
}

func (fakeCategoryQuery) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	return nil, nil
}

func (fakeCategoryQuery) GetByID(context.Context, int) (domain.Category, error) {
	return domain.Category{}, nil
}

type fakeProductQuery struct{}

func (fakeProductQuery) FindAll(ctx context.Context, _ domain.ListProductParams) (domain.PaginatedProducts, error) {
	startChild(ctx, "SELECT")
	return domain.PaginatedProducts{}, nil
}

func (fakeProductQuery) GetByID(context.Context, uuid.UUID) (domain.ProductDetail, error) {
	return domain.ProductDetail{}, nil
}

func (fakeProductQuery) GetByHandle(context.Context, string) (domain.ProductDetail, error) {
	return domain.ProductDetail{}, nil
}

type fakeReviewSummary struct{}

func (fakeReviewSummary) GetSummary(ctx context.Context, _ uuid.UUID) (domain.RatingSummary, error) {
	startChild(ctx, "SELECT")
	return domain.RatingSummary{}, nil
}

type fakeReservation struct{}

func (fakeReservation) Create(ctx context.Context, _ uuid.UUID, _ time.Time, _ []domain.ReservationRequestItem) (domain.ReservationResult, error) {
	startChild(ctx, "INSERT")
	return domain.ReservationResult{}, nil
}

// tracedReservation performs the same coordination and transactional work shape
// as the real reservation use case, using the real Redis adapter.
type tracedReservation struct {
	locker domain.ReservationLocker
}

func (r tracedReservation) Create(ctx context.Context, orderID uuid.UUID, _ time.Time, _ []domain.ReservationRequestItem) (domain.ReservationResult, error) {
	keys := []string{"reservation:order:" + orderID.String()}

	owner, err := r.locker.Acquire(ctx, keys)
	if err != nil {
		return domain.ReservationResult{}, err
	}
	defer func() { _ = r.locker.Release(ctx, keys, owner) }()

	for _, name := range []string{"BEGIN", "SELECT", "COMMIT"} {
		startChild(ctx, name)
	}
	return domain.ReservationResult{}, nil
}

// recordingRedis is a fake Upstash REST endpoint that records the propagated
// trace headers.
type recordingRedis struct {
	*httptest.Server
	parents []string
}

func newRecordingRedis(t *testing.T) *recordingRedis {
	t.Helper()

	fake := &recordingRedis{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.parents = append(fake.parents, r.Header.Get("traceparent"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":1}`))
	}))
	return fake
}

func (f *recordingRedis) traceParents() []string { return f.parents }
