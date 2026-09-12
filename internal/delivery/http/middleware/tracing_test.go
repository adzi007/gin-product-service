package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	upstreamTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	upstreamTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	upstreamSpanID      = "00f067aa0ba902b7"
)

// testTracing wires a recorder-backed provider and a router covering every
// coverage-policy surface.
type testTracing struct {
	router   *gin.Engine
	recorder *tracetest.SpanRecorder
	enriched int
}

func newTestTracing(t *testing.T, parentBased bool) *testTracing {
	t.Helper()
	return newTestTracingWithStatus(t, parentBased, http.StatusOK)
}

func newTestTracingWithStatus(t *testing.T, parentBased bool, status int) *testTracing {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := tracetest.NewSpanRecorder()
	sampler := sdktrace.Sampler(sdktrace.AlwaysSample())
	if parentBased {
		sampler = sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	state := &testTracing{recorder: recorder}

	router := gin.New()
	router.Use(Tracing(TracingConfig{
		Enabled:        true,
		TracerProvider: provider,
		Propagator:     propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
		EnrichContext: func(ctx context.Context) context.Context {
			state.enriched++
			return ctx
		},
	}))

	handler := func(c *gin.Context) { c.Status(status) }
	router.GET("/api/v1/products/:id", handler)
	router.GET("/api/v1/categories", handler)
	router.GET("/readyz", handler)
	router.GET("/healthz", handler)
	router.GET("/metrics", handler)
	router.GET("/swagger/*any", handler)

	state.router = router
	return state
}

func (s *testTracing) do(t *testing.T, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func (s *testTracing) doWithContext(t *testing.T, ctx context.Context, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func (s *testTracing) ended() []sdktrace.ReadOnlySpan {
	return s.recorder.Ended()
}

func attributeValue(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.Emit(), true
		}
	}
	return "", false
}

func TestTracingSuccessPathCreatesServerSpan(t *testing.T) {
	state := newTestTracing(t, false)

	rec := state.do(t, http.MethodGet, "/api/v1/products/9f1c", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	spans := state.ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	span := spans[0]

	if got := span.Name(); got != "HTTP GET /api/v1/products/:id" {
		t.Fatalf("span name = %q, want the matched route template", got)
	}
	if got := span.SpanKind(); got != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want server", got)
	}
	if got, ok := attributeValue(span, "http.request.method"); !ok || got != http.MethodGet {
		t.Fatalf("http.request.method = %q (present=%v), want GET", got, ok)
	}
	if got, ok := attributeValue(span, "http.route"); !ok || got != "/api/v1/products/:id" {
		t.Fatalf("http.route = %q (present=%v), want the route template", got, ok)
	}
	if got, ok := attributeValue(span, "http.response.status_code"); !ok || got != "200" {
		t.Fatalf("http.response.status_code = %q (present=%v), want 200", got, ok)
	}
	if span.Parent().IsValid() {
		t.Fatalf("root span has a parent: %v", span.Parent())
	}
	if state.enriched != 1 {
		t.Fatalf("context enrichment calls = %d, want 1", state.enriched)
	}
}

// Raw URL paths and identifiers must never become span names or attributes.
func TestTracingUsesRouteTemplateNotRawPath(t *testing.T) {
	state := newTestTracing(t, false)

	const rawID = "order-8f14e45fceea167a5a36dedd4bea2543"
	state.do(t, http.MethodGet, "/api/v1/products/"+rawID, nil)

	spans := state.ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	span := spans[0]

	if span.Name() == "HTTP GET /api/v1/products/"+rawID {
		t.Fatalf("span name leaked the raw path")
	}
	for _, attr := range span.Attributes() {
		if attr.Value.AsString() == rawID {
			t.Fatalf("attribute %q leaked the raw identifier", attr.Key)
		}
	}
}

func TestTracingCoveragePolicy(t *testing.T) {
	tests := []struct {
		path      string
		wantSpans int
	}{
		{"/api/v1/products/1", 1},
		{"/api/v1/categories", 1},
		{"/readyz", 1},
		{"/healthz", 0},
		{"/metrics", 0},
		{"/swagger/index.html", 0},
		{"/api/v1/unmatched", 0},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			state := newTestTracing(t, false)
			state.do(t, http.MethodGet, tt.path, nil)

			if got := len(state.ended()); got != tt.wantSpans {
				t.Fatalf("recorded spans = %d, want %d", got, tt.wantSpans)
			}
		})
	}
}

func TestTracingContinuesValidUpstreamContext(t *testing.T) {
	state := newTestTracing(t, false)

	state.do(t, http.MethodGet, "/api/v1/categories", map[string]string{"traceparent": upstreamTraceParent})

	spans := state.ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	span := spans[0]

	if got := span.SpanContext().TraceID().String(); got != upstreamTraceID {
		t.Fatalf("trace ID = %q, want the upstream trace %q", got, upstreamTraceID)
	}
	wantParentSpanID, err := trace.SpanIDFromHex(upstreamSpanID)
	if err != nil {
		t.Fatalf("SpanIDFromHex(): %v", err)
	}
	if got := span.Parent().SpanID(); got != wantParentSpanID {
		t.Fatalf("parent span ID = %q, want %q", got, wantParentSpanID)
	}
	if !span.Parent().IsRemote() {
		t.Fatalf("parent should be remote")
	}
}

func TestTracingHonorsUpstreamNonSampling(t *testing.T) {
	state := newTestTracing(t, true)

	rec := state.do(t, http.MethodGet, "/api/v1/categories", map[string]string{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: sampling must not change the response", rec.Code, http.StatusOK)
	}
	if got := len(state.ended()); got != 0 {
		t.Fatalf("recorded spans = %d, want 0 for an unsampled upstream parent", got)
	}
}

func TestTracingMalformedContextStartsNewTrace(t *testing.T) {
	state := newTestTracing(t, true)

	rec := state.do(t, http.MethodGet, "/api/v1/categories", map[string]string{"traceparent": "malformed"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: malformed trace metadata must not reject the request", rec.Code, http.StatusOK)
	}

	spans := state.ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1 new root span", len(spans))
	}
	if spans[0].Parent().IsValid() {
		t.Fatalf("span should be a new root, got parent %v", spans[0].Parent())
	}
}

func TestTracingDisabledCreatesNoSpans(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	router := gin.New()
	router.Use(Tracing(TracingConfig{Enabled: false}))
	router.GET("/api/v1/categories", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := len(recorder.Ended()); got != 0 {
		t.Fatalf("recorded spans = %d, want 0 while disabled", got)
	}
}

// Expected 4xx responses must keep their exact HTTP outcome without becoming
// trace errors; 5xx responses must be errors.
func TestTracingStatusRules(t *testing.T) {
	tests := []struct {
		status    int
		wantError bool
	}{
		{http.StatusOK, false},
		{http.StatusCreated, false},
		{http.StatusMovedPermanently, false},
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusNotFound, false},
		{http.StatusUnprocessableEntity, false},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			state := newTestTracingWithStatus(t, false, tt.status)

			rec := state.do(t, http.MethodGet, "/api/v1/categories", nil)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}

			spans := state.ended()
			if len(spans) != 1 {
				t.Fatalf("recorded spans = %d, want 1", len(spans))
			}
			span := spans[0]

			if got, ok := attributeValue(span, "http.response.status_code"); !ok || got != strconv.Itoa(tt.status) {
				t.Fatalf("http.response.status_code = %q (present=%v), want %d", got, ok, tt.status)
			}
			if got := span.Status().Code == codes.Error; got != tt.wantError {
				t.Fatalf("span error status = %v, want %v (description=%q)", got, tt.wantError, span.Status().Description)
			}
		})
	}
}

// Cancelled, deadline-exceeded, and client-disconnected requests are abnormal
// interruptions: the span is closed, marked error, and classified with a
// bounded interruption type.
func TestTracingInterruptionRules(t *testing.T) {
	tests := []struct {
		name          string
		ctx           func() context.Context
		wantInterrupt string
	}{
		{
			name: "cancelled client",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantInterrupt: "canceled",
		},
		{
			name: "deadline exceeded",
			ctx: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				return ctx
			},
			wantInterrupt: "deadline_exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newTestTracingWithStatus(t, false, http.StatusOK)

			state.doWithContext(t, tt.ctx(), http.MethodGet, "/api/v1/categories")

			spans := state.ended()
			if len(spans) != 1 {
				t.Fatalf("recorded spans = %d, want 1 closed span", len(spans))
			}
			span := spans[0]

			if span.Status().Code != codes.Error {
				t.Fatalf("span status = %v, want error for an interruption", span.Status().Code)
			}

			var got string
			for _, event := range span.Events() {
				for _, attr := range event.Attributes {
					if string(attr.Key) == attrInterruptionType {
						got = attr.Value.Emit()
					}
				}
			}
			if got != tt.wantInterrupt {
				t.Fatalf("interruption type = %q, want %q", got, tt.wantInterrupt)
			}
		})
	}
}

// Interruption classification must be constant and must never carry the raw
// transport error text.
func TestTracingInterruptionAttributesAreConstant(t *testing.T) {
	state := newTestTracing(t, false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state.doWithContext(t, ctx, http.MethodGet, "/api/v1/categories")

	spans := state.ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}

	for _, event := range spans[0].Events() {
		if event.Name != eventRequestInterrupted {
			t.Errorf("event name = %q, want the constant %q", event.Name, eventRequestInterrupted)
		}
		for _, attr := range event.Attributes {
			if string(attr.Key) != attrInterruptionType {
				t.Errorf("unexpected event attribute %q", attr.Key)
			}
			if attr.Value.Emit() != "canceled" {
				t.Errorf("interruption type = %q, want a bounded value", attr.Value.Emit())
			}
		}
	}
}
