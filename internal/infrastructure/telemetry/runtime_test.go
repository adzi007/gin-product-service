package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/infrastructure/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

const testProtocolContentType = "application/x-protobuf"

// enabledConfig builds a valid enabled configuration for tests without going
// through environment parsing.
func enabledConfig(endpoint string) TelemetryConfig {
	return TelemetryConfig{
		Enabled:            true,
		ServiceName:        "gin-product-service",
		Environment:        "test",
		Endpoint:           endpoint,
		Compression:        CompressionNone,
		ExporterTimeout:    2 * time.Second,
		RootSampleRatio:    1,
		QueueSize:          64,
		BatchSize:          8,
		ScheduleDelay:      50 * time.Millisecond,
		BatchExportTimeout: 2 * time.Second,
		ShutdownTimeout:    2 * time.Second,
	}
}

func remoteSpanContext(sampled bool) trace.SpanContext {
	traceID, _ := trace.TraceIDFromHex(validTraceID)
	spanID, _ := trace.SpanIDFromHex(validSpanID)
	flags := trace.TraceFlags(0)
	if sampled {
		flags = trace.FlagsSampled
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: flags,
		Remote:     true,
	})
}

func TestRuntimeDisabledIsNoOp(t *testing.T) {
	cfg, err := ParseConfig(envFunc(nil))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	rt, err := NewRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if rt.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if rt.Propagator == nil {
		t.Fatalf("Propagator = nil, want an explicit no-op propagator")
	}
	// No provider/exporter may be constructed while disabled.
	if _, ok := rt.TracerProvider.(*sdktrace.TracerProvider); ok {
		t.Fatalf("TracerProvider = %T, want the no-op provider when disabled", rt.TracerProvider)
	}

	// No trace context may be extracted and no trace work attempted.
	ctx := rt.Propagator.Extract(context.Background(), propagation.MapCarrier{"traceparent": validTraceParent})
	if trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatalf("disabled propagator extracted a trace context")
	}

	_, span := rt.TracerProvider.Tracer("test").Start(context.Background(), "operation")
	if span.SpanContext().IsValid() {
		t.Fatalf("disabled runtime started a recording span")
	}
	span.End()

	for i := 0; i < 2; i++ {
		if err := rt.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown() call %d error = %v, want nil", i+1, err)
		}
	}
}

func TestRuntimeParentBasedSampler(t *testing.T) {
	sampledParent := remoteSpanContext(true)
	unsampledParent := remoteSpanContext(false)

	tests := []struct {
		name        string
		ratio       float64
		parent      *trace.SpanContext
		wantSampled bool
	}{
		{"new root sampled at ratio 1", 1, nil, true},
		{"new root not sampled at ratio 0", 0, nil, false},
		{"upstream sampled parent honored at ratio 0", 0, &sampledParent, true},
		{"upstream unsampled parent honored at ratio 1", 1, &unsampledParent, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver := newCaptureReceiver(t, http.StatusOK)
			cfg := enabledConfig(receiver.endpoint())
			cfg.RootSampleRatio = tt.ratio

			rt, err := NewRuntime(context.Background(), cfg)
			if err != nil {
				t.Fatalf("NewRuntime() error = %v", err)
			}

			ctx := context.Background()
			if tt.parent != nil {
				ctx = trace.ContextWithRemoteSpanContext(ctx, *tt.parent)
			}
			_, span := rt.TracerProvider.Tracer("test").Start(ctx, "operation")
			if got := span.SpanContext().IsSampled(); got != tt.wantSampled {
				t.Fatalf("sampled = %v, want %v", got, tt.wantSampled)
			}
			span.End()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := rt.Shutdown(shutdownCtx); err != nil {
				t.Fatalf("Shutdown() error = %v", err)
			}
		})
	}
}

func TestRuntimeResourceIdentity(t *testing.T) {
	receiver := newCaptureReceiver(t, http.StatusOK)
	cfg := enabledConfig(receiver.endpoint())
	cfg.Environment = "staging"

	rt, err := NewRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	_, span := rt.TracerProvider.Tracer("test").Start(context.Background(), "operation")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	requests := receiver.snapshot()
	if len(requests) == 0 {
		t.Fatalf("no export request reached the capture receiver")
	}
	decoded := decodeExportRequests(t, requests)
	for _, request := range decoded {
		for _, resourceSpans := range request.GetResourceSpans() {
			attrs := attributeMap(resourceSpans.GetResource().GetAttributes())
			if got := attrs["service.name"]; got != cfg.ServiceName {
				t.Errorf("service.name = %q, want %q", got, cfg.ServiceName)
			}
			if got := attrs["deployment.environment.name"]; got != cfg.Environment {
				t.Errorf("deployment.environment.name = %q, want %q", got, cfg.Environment)
			}
		}
	}
}

// TestOTLPHTTPExporter is the required exporter contract test: it crosses the
// real OTLP/HTTP serialization boundary against a local capture receiver.
func TestOTLPHTTPExporter(t *testing.T) {
	receiver := newCaptureReceiver(t, http.StatusOK)
	cfg := enabledConfig(receiver.endpoint())
	warningSinkMessages := make([]string, 0, 1)

	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	rt, err := NewRuntime(context.Background(), cfg,
		WithTelemetryMetrics(runtimeMetrics),
		WithWarningSink(func(message string) { warningSinkMessages = append(warningSinkMessages, message) }),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	_, span := rt.TracerProvider.Tracer("test").Start(context.Background(), "HTTP GET /api/v1/products")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want a successful flush", err)
	}

	requests := receiver.snapshot()
	if len(requests) == 0 {
		t.Fatalf("exporter sent no request to the capture receiver")
	}

	var sawTraceRequest bool
	for _, request := range requests {
		if request.method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.method)
			continue
		}
		if request.path != "/v1/traces" {
			t.Errorf("path = %q, want /v1/traces", request.path)
			continue
		}
		if !strings.Contains(request.contentType, testProtocolContentType) {
			t.Errorf("Content-Type = %q, want %q", request.contentType, testProtocolContentType)
		}

		decoded, err := decodeExportRequest(request)
		if err != nil {
			t.Fatalf("body did not decode as an OTLP trace export request: %v", err)
		}
		if len(decoded.GetResourceSpans()) == 0 {
			t.Fatalf("decoded request contains no resource spans")
		}

		var spanNames []string
		for _, resourceSpans := range decoded.GetResourceSpans() {
			for _, scopeSpans := range resourceSpans.GetScopeSpans() {
				for _, decodedSpan := range scopeSpans.GetSpans() {
					spanNames = append(spanNames, decodedSpan.GetName())
				}
			}
		}
		if len(spanNames) == 0 {
			t.Fatalf("decoded request contains no spans")
		}
		for _, name := range spanNames {
			if name == "HTTP GET /api/v1/products" {
				sawTraceRequest = true
			}
		}
	}

	if !sawTraceRequest {
		t.Fatalf("decoded payload did not contain the emitted span name")
	}
	if len(warningSinkMessages) != 0 {
		t.Fatalf("unexpected warnings for a successful export: %v", warningSinkMessages)
	}
	if got := testutil.ToFloat64(runtimeMetrics.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureServerError))); got != 0 {
		t.Fatalf("exporter failure counter = %v, want 0", got)
	}
}

func TestOTLPHTTPExporterNonSuccessResponseIsSafe(t *testing.T) {
	receiver := newCaptureReceiver(t, http.StatusBadRequest)
	cfg := enabledConfig(receiver.endpoint())

	var warnings []string
	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	rt, err := NewRuntime(context.Background(), cfg,
		WithTelemetryMetrics(runtimeMetrics),
		WithWarningSink(func(message string) { warnings = append(warnings, message) }),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	_, span := rt.TracerProvider.Tracer("test").Start(context.Background(), "operation")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Fail-open: a rejected export must never surface as a business/startup error.
	if err := rt.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil (fail-open)", err)
	}

	if got := testutil.ToFloat64(runtimeMetrics.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureServerError))); got < 1 {
		t.Fatalf("server_error failure count = %v, want at least 1", got)
	}
	if len(warnings) == 0 {
		t.Fatalf("want a sanitized warning for the failed export")
	}
	for _, warning := range warnings {
		if strings.Contains(warning, receiver.server.URL) || strings.Contains(warning, "127.0.0.1") {
			t.Fatalf("warning %q leaks the destination", warning)
		}
	}
}

func TestOTLPHTTPExporterUnavailableReceiverIsSafe(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := unavailable.URL + "/v1/traces"
	unavailable.Close()

	cfg := enabledConfig(endpoint)

	var warnings []string
	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	rt, err := NewRuntime(context.Background(), cfg,
		WithTelemetryMetrics(runtimeMetrics),
		WithWarningSink(func(message string) { warnings = append(warnings, message) }),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	_, span := rt.TracerProvider.Tracer("test").Start(context.Background(), "operation")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil (fail-open)", err)
	}

	if got := testutil.ToFloat64(runtimeMetrics.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTransport))); got < 1 {
		t.Fatalf("transport failure count = %v, want at least 1", got)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, endpoint) {
			t.Fatalf("warning %q leaks the destination", warning)
		}
	}
}

func TestRuntimeShutdownIsIdempotent(t *testing.T) {
	receiver := newCaptureReceiver(t, http.StatusOK)
	cfg := enabledConfig(receiver.endpoint())

	rt, err := NewRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}

	start := time.Now()
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("second Shutdown took %v, want it to return immediately", elapsed)
	}
}

func TestNewRuntimeRejectsInvalidEnabledConfiguration(t *testing.T) {
	cfg := enabledConfig("not-a-url")
	cfg.Endpoint = "not-a-url"

	if _, err := NewRuntime(context.Background(), cfg); err == nil {
		t.Fatalf("NewRuntime() error = nil, want the validated configuration rejected")
	}
}

// captureReceiver is a local OTLP/HTTP endpoint that records every request.
type captureReceiver struct {
	server *httptest.Server
	status int

	mu       sync.Mutex
	requests []capturedRequest
}

type capturedRequest struct {
	method      string
	path        string
	contentType string
	encoding    string
	body        []byte
}

func newCaptureReceiver(t *testing.T, status int) *captureReceiver {
	t.Helper()

	receiver := &captureReceiver{status: status}
	receiver.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)

		receiver.mu.Lock()
		receiver.requests = append(receiver.requests, capturedRequest{
			method:      req.Method,
			path:        req.URL.Path,
			contentType: req.Header.Get("Content-Type"),
			encoding:    req.Header.Get("Content-Encoding"),
			body:        body,
		})
		receiver.mu.Unlock()

		payload, _ := proto.Marshal(&coltracepb.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", testProtocolContentType)
		w.WriteHeader(receiver.status)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(receiver.server.Close)

	return receiver
}

func (r *captureReceiver) endpoint() string { return r.server.URL + "/v1/traces" }

func (r *captureReceiver) snapshot() []capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capturedRequest, len(r.requests))
	copy(out, r.requests)
	return out
}

func decodeExportRequests(t *testing.T, requests []capturedRequest) []*coltracepb.ExportTraceServiceRequest {
	t.Helper()

	decoded := make([]*coltracepb.ExportTraceServiceRequest, 0, len(requests))
	for _, request := range requests {
		message, err := decodeExportRequest(request)
		if err != nil {
			t.Fatalf("body did not decode as an OTLP trace export request: %v", err)
		}
		decoded = append(decoded, message)
	}
	return decoded
}

func decodeExportRequest(request capturedRequest) (*coltracepb.ExportTraceServiceRequest, error) {
	message := &coltracepb.ExportTraceServiceRequest{}
	if err := proto.Unmarshal(request.body, message); err != nil {
		return nil, err
	}
	return message, nil
}

func attributeMap(attrs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[attr.GetKey()] = attr.GetValue().GetStringValue()
	}
	return out
}
