package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newRecoveryRouter composes tracing with the tracing-aware recovery, in the
// same order the router registers them.
func newRecoveryRouter(t *testing.T, register func(*gin.Engine)) (*gin.Engine, *tracetest.SpanRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	router := gin.New()
	router.Use(Tracing(TracingConfig{
		Enabled:        true,
		TracerProvider: provider,
		Propagator:     propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
	}))
	router.Use(Recovery(RecoveryConfig{}))
	register(router)

	return router, recorder
}

func TestRecoveryClosesSpanAndPreserves500Response(t *testing.T) {
	const panicSecret = "sup3r-s3cret-panic-value"

	router, recorder := newRecoveryRouter(t, func(router *gin.Engine) {
		router.GET("/api/v1/boom", func(*gin.Context) {
			panic(panicSecret)
		})
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (the established 500 contract)", rec.Code, http.StatusInternalServerError)
	}
	if body := rec.Body.String(); body != "" {
		t.Fatalf("body = %q, want an empty body like the previous recovery behavior", body)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want exactly 1 closed server span", len(spans))
	}
	span := spans[0]

	if span.Status().Code != codes.Error {
		t.Fatalf("span status = %v, want error after a recovered panic", span.Status().Code)
	}

	var sawConstantEvent bool
	for _, event := range span.Events() {
		if event.Name == eventPanicRecovered {
			sawConstantEvent = true
			if len(event.Attributes) != 0 {
				t.Fatalf("panic event carried attributes: %v", event.Attributes)
			}
		}
	}
	if !sawConstantEvent {
		t.Fatalf("no constant %q event was recorded; events: %v", eventPanicRecovered, span.Events())
	}

	// The panic value must never be exported.
	values := []string{span.Status().Description}
	for _, attr := range span.Attributes() {
		values = append(values, attr.Value.Emit())
	}
	for _, event := range span.Events() {
		values = append(values, event.Name)
		for _, attr := range event.Attributes {
			values = append(values, attr.Value.Emit())
		}
	}
	for _, value := range values {
		if strings.Contains(value, panicSecret) {
			t.Fatalf("exported value %q leaks the panic value", value)
		}
	}
}

func TestRecoveryLeavesSuccessfulRequestsUntouched(t *testing.T) {
	router, recorder := newRecoveryRouter(t, func(router *gin.Engine) {
		router.GET("/api/v1/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ok", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	if spans[0].Status().Code == codes.Error {
		t.Fatalf("successful request marked as an error")
	}
	for _, event := range spans[0].Events() {
		if event.Name == eventPanicRecovered {
			t.Fatalf("successful request recorded a panic event")
		}
	}
}

// Untraced routes keep their behavior and produce no span, but still return 500.
func TestRecoveryPreservesBehaviorOnUntracedRoutes(t *testing.T) {
	router, recorder := newRecoveryRouter(t, func(router *gin.Engine) {
		router.GET("/healthz", func(*gin.Context) {
			panic("boom")
		})
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := len(recorder.Ended()); got != 0 {
		t.Fatalf("recorded spans = %d, want 0 for an excluded route", got)
	}
}

func TestRecoveryReportsPanicThroughSanitizedHook(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reports := 0
	router := gin.New()
	router.Use(Recovery(RecoveryConfig{Report: func(*gin.Context) { reports++ }}))
	router.GET("/boom", func(*gin.Context) { panic("boom") })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if reports != 1 {
		t.Fatalf("reports = %d, want 1", reports)
	}
}
