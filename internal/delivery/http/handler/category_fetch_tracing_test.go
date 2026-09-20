package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	categoryapp "gin-product-service/internal/app/category"
	"gin-product-service/internal/delivery/http/middleware"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/telemetry"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCategoryFetchErrorReturnsTraceIDAndLayeredSpans(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	cause := errors.New("category repository unavailable")
	queryUseCase := categoryapp.NewCategoryQueryUseCase(failingCategoryRepository{err: cause})
	handler := NewCategoryHandler(queryUseCase, nil, nil, nil)

	engine := gin.New()
	engine.Use(middleware.Tracing(middleware.TracingConfig{
		Enabled:        true,
		TracerProvider: provider,
		Propagator:     telemetry.NewPropagator(nil),
		EnrichContext:  logger.WithTraceContext,
	}))
	engine.GET("/api/v1/categories", handler.Fetch)

	recorderHTTP := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/categories?page=1", nil)
	engine.ServeHTTP(recorderHTTP, request)

	if recorderHTTP.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorderHTTP.Code, http.StatusInternalServerError)
	}

	var body map[string]string
	if err := json.Unmarshal(recorderHTTP.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["trace_id"] == "" {
		t.Fatal("error response trace_id is empty")
	}

	spans := recorder.Ended()
	server := findRecordedSpan(t, spans, "HTTP GET /api/v1/categories")
	controller := findRecordedSpan(t, spans, "controller.category.fetch")
	useCase := findRecordedSpan(t, spans, "usecase.category.find_all")

	if body["trace_id"] != server.SpanContext().TraceID().String() {
		t.Errorf("response trace_id = %q, want %q", body["trace_id"], server.SpanContext().TraceID())
	}
	if controller.Parent().SpanID() != server.SpanContext().SpanID() {
		t.Errorf("controller parent = %q, want HTTP span %q", controller.Parent().SpanID(), server.SpanContext().SpanID())
	}
	if useCase.Parent().SpanID() != controller.SpanContext().SpanID() {
		t.Errorf("use-case parent = %q, want controller span %q", useCase.Parent().SpanID(), controller.SpanContext().SpanID())
	}

	for _, span := range []sdktrace.ReadOnlySpan{server, controller, useCase} {
		if span.Status().Code != codes.Error {
			t.Errorf("span %q status = %v, want error", span.Name(), span.Status().Code)
		}
	}
	if !hasExceptionEvent(controller) || !hasExceptionEvent(useCase) {
		t.Error("controller and use-case spans must record exception events")
	}
}

func findRecordedSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("span %q not found", name)
	return nil
}

func hasExceptionEvent(span sdktrace.ReadOnlySpan) bool {
	for _, event := range span.Events() {
		if event.Name == "exception" {
			return true
		}
	}
	return false
}

type failingCategoryRepository struct {
	err error
}

func (r failingCategoryRepository) FindAll(context.Context, domain.ListCategoryParams) ([]domain.Category, int, error) {
	return nil, 0, r.err
}

func (failingCategoryRepository) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	return nil, nil
}

func (failingCategoryRepository) Create(context.Context, domain.Category) (domain.Category, error) {
	return domain.Category{}, nil
}

func (failingCategoryRepository) FindByID(context.Context, int) (domain.Category, error) {
	return domain.Category{}, nil
}

func (failingCategoryRepository) Update(context.Context, int, domain.Category) (domain.Category, error) {
	return domain.Category{}, nil
}

func (failingCategoryRepository) Delete(context.Context, int) error { return nil }
