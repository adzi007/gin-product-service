package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/infrastructure/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

const testProtocolContentType = "application/x-protobuf"

// enabledConfig builds a valid enabled configuration for tests without going
// through environment parsing, and pins deterministic native sampling.
func enabledConfig(t *testing.T, endpoint string) TelemetryConfig {
	t.Helper()
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.0")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")
	return TelemetryConfig{
		Enabled:         true,
		Environment:     "test",
		Endpoint:        endpoint,
		ShutdownTimeout: 2 * time.Second,
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
		arg         string
		parent      *trace.SpanContext
		wantSampled bool
	}{
		{"new root sampled at ratio 1", "1", nil, true},
		{"new root not sampled at ratio 0", "0", nil, false},
		{"upstream sampled parent honored at ratio 0", "0", &sampledParent, true},
		{"upstream unsampled parent honored at ratio 1", "1", &unsampledParent, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver := newCaptureReceiver(t, http.StatusOK)
			cfg := enabledConfig(t, receiver.endpoint())
			t.Setenv("OTEL_TRACES_SAMPLER_ARG", tt.arg)

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
	cfg := enabledConfig(t, receiver.endpoint())
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
			if got := attrs["service.name"]; got != DefaultServiceName {
				t.Errorf("service.name = %q, want %q", got, DefaultServiceName)
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
	cfg := enabledConfig(t, receiver.endpoint())
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
	cfg := enabledConfig(t, receiver.endpoint())

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

	cfg := enabledConfig(t, endpoint)

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
	cfg := enabledConfig(t, receiver.endpoint())

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
	cfg := enabledConfig(t, "not-a-url")
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

// The D1-approved drop bridge owns process-global state only for the lifetime of
// an enabled runtime and restores it at shutdown.
func TestRuntimeInstallsAndRestoresObservabilityGlobals(t *testing.T) {
	prevMP := otel.GetMeterProvider()
	prevObs, hadObs := os.LookupEnv(observabilityEnvKey)
	t.Cleanup(func() {
		otel.SetMeterProvider(prevMP)
		if hadObs {
			_ = os.Setenv(observabilityEnvKey, prevObs)
		} else {
			_ = os.Unsetenv(observabilityEnvKey)
		}
	})

	receiver := newCaptureReceiver(t, http.StatusOK)
	cfg := enabledConfig(t, receiver.endpoint())

	rt, err := NewRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	if got := os.Getenv(observabilityEnvKey); got != "true" {
		t.Fatalf("observability flag = %q, want %q while the enabled runtime lives", got, "true")
	}
	if _, ok := otel.GetMeterProvider().(*DropObservationMeterProvider); !ok {
		t.Fatalf("global meter provider = %T, want the drop bridge", otel.GetMeterProvider())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if hadObs {
		if got := os.Getenv(observabilityEnvKey); got != prevObs {
			t.Fatalf("observability flag = %q after shutdown, want restored %q", got, prevObs)
		}
	} else if _, ok := os.LookupEnv(observabilityEnvKey); ok {
		t.Fatalf("observability flag was not unset after shutdown")
	}
	if otel.GetMeterProvider() != prevMP {
		t.Fatalf("global meter provider was not restored after shutdown")
	}
}

// The bridge counter must count concurrent native drops exactly once each.
func TestDropObservationCounterConcurrentAddsCountExactly(t *testing.T) {
	const (
		workers = 32
		each    = 100
	)

	m := newTestMetrics(t)
	warnings := NewWarningDispatcher(NewWarningLimiter(time.Hour), nil)
	defer warnings.Stop()

	bridge := NewDropObservationMeterProvider(m, warnings)
	counter, err := bridge.Meter("go.opentelemetry.io/otel/sdk/trace/internal/observ").Int64Counter(sdkSpanProcessedInstrument)
	if err != nil {
		t.Fatalf("Int64Counter() error = %v", err)
	}

	queueFull := metric.WithAttributes(
		attribute.String("otel.component.type", "batching_span_processor"),
		attribute.String("otel.component.name", "batching_span_processor/0"),
		attribute.String("error.type", "queue_full"),
	)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				counter.Add(context.Background(), 1, queueFull)
			}
		}()
	}
	wg.Wait()

	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != workers*each {
		t.Fatalf("dropped = %v, want %d concurrent queue-full drops counted exactly once", got, workers*each)
	}
}

// unsetEnv removes key from the environment for the duration of the test and
// restores its previous value (or absence) afterward. Tests using this must not
// call t.Parallel.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	prev, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%q): %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// The runtime bootstraps native parent-based sampling with the documented 0.10
// default when the operator leaves the sampler unset, and restores the
// process environment at shutdown.
func TestRuntimeBootstrapsDefaultSamplingAndRestoresIt(t *testing.T) {
	unsetEnv(t, "OTEL_TRACES_SAMPLER")
	unsetEnv(t, "OTEL_TRACES_SAMPLER_ARG")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")

	receiver := newCaptureReceiver(t, http.StatusOK)
	cfg := TelemetryConfig{
		Enabled:         true,
		Environment:     "test",
		Endpoint:        receiver.endpoint(),
		ShutdownTimeout: 2 * time.Second,
	}

	rt, err := NewRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	if got := os.Getenv("OTEL_TRACES_SAMPLER"); got != "parentbased_traceidratio" {
		t.Fatalf("OTEL_TRACES_SAMPLER = %q, want parentbased_traceidratio bootstrap", got)
	}
	if got := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); got != "0.10" {
		t.Fatalf("OTEL_TRACES_SAMPLER_ARG = %q, want 0.10 bootstrap", got)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if _, ok := os.LookupEnv("OTEL_TRACES_SAMPLER"); ok {
		t.Fatalf("OTEL_TRACES_SAMPLER was not unset after shutdown")
	}
	if _, ok := os.LookupEnv("OTEL_TRACES_SAMPLER_ARG"); ok {
		t.Fatalf("OTEL_TRACES_SAMPLER_ARG was not unset after shutdown")
	}
}

// Malformed native tuning values and header sentinels are delegated to the SDK:
// startup must not panic or fail on them, and parsing diagnostics must never be
// counted as exporter failures.
func TestRuntimeToleratesMalformedNativeTuning(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.0")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")
	t.Setenv("OTEL_BSP_MAX_QUEUE_SIZE", "not-a-number")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=Bearer sup3r-s3cret-header")

	receiver := newCaptureReceiver(t, http.StatusOK)
	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	cfg := TelemetryConfig{
		Enabled:         true,
		Environment:     "test",
		Endpoint:        receiver.endpoint(),
		ShutdownTimeout: 2 * time.Second,
	}

	rt, err := NewRuntime(context.Background(), cfg, WithTelemetryMetrics(runtimeMetrics))
	if err != nil {
		t.Fatalf("NewRuntime() error = %v, want native malformed tuning tolerated", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if got := testutil.ToFloat64(runtimeMetrics.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureOtherReason))); got != 0 {
		t.Fatalf("native parsing diagnostic was counted as an exporter failure: %v", got)
	}
}
