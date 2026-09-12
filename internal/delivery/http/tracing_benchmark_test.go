package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"gin-product-service/internal/delivery/http/middleware"
	"gin-product-service/internal/infrastructure/telemetry"

	"github.com/gin-gonic/gin"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// discardProcessor keeps span creation on the hot path without accumulating
// exports in memory, so benchmarks measure tracing overhead rather than test
// bookkeeping.
type discardProcessor struct{}

func (discardProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (discardProcessor) OnEnd(sdktrace.ReadOnlySpan) {}

func (discardProcessor) Shutdown(context.Context) error { return nil }

func (discardProcessor) ForceFlush(context.Context) error { return nil }

func benchmarkRequest(b *testing.B, enabled bool) {
	b.Helper()
	gin.SetMode(gin.ReleaseMode)

	config := middleware.TracingConfig{}
	var provider trace.TracerProvider
	if enabled {
		sdkProvider := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithSpanProcessor(discardProcessor{}),
		)
		defer func() { _ = sdkProvider.Shutdown(context.Background()) }()

		provider = sdkProvider
		config = middleware.TracingConfig{
			Enabled:        true,
			TracerProvider: provider,
			Propagator:     telemetry.NewPropagator(nil),
		}
	}

	engine := gin.New()
	engine.Use(middleware.Tracing(config))
	engine.GET("/api/v1/categories", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.ServeHTTP(httptest.NewRecorder(), request)
	}
}

// BenchmarkRequestTracingDisabled is the SC-005 baseline.
func BenchmarkRequestTracingDisabled(b *testing.B) {
	benchmarkRequest(b, false)
}

// BenchmarkRequestTracingEnabled measures the added latency and allocations.
// Compare with BenchmarkRequestTracingDisabled to confirm the p95 overhead stays
// within the documented budget under production sampling.
func BenchmarkRequestTracingEnabled(b *testing.B) {
	benchmarkRequest(b, true)
}

// Tracing must not grow background work without bound, even across many
// requests, which is the resource half of SC-005.
func TestTracingKeepsBackgroundWorkBounded(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(discardProcessor{}),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	engine := gin.New()
	engine.Use(middleware.Tracing(middleware.TracingConfig{
		Enabled:        true,
		TracerProvider: provider,
		Propagator:     telemetry.NewPropagator(nil),
	}))
	engine.GET("/api/v1/categories", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)

	// Warm up before sampling the goroutine count.
	for i := 0; i < 10; i++ {
		engine.ServeHTTP(httptest.NewRecorder(), request)
	}
	before := runtime.NumGoroutine()

	const requests = 500
	for i := 0; i < requests; i++ {
		engine.ServeHTTP(httptest.NewRecorder(), request)
	}

	runtime.GC()
	after := runtime.NumGoroutine()
	if grown := after - before; grown > 10 {
		t.Fatalf("goroutines grew by %d across %d traced requests, want bounded background work", grown, requests)
	}
}
