package telemetry

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
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
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func newTestMetrics(t *testing.T) *metrics.TelemetryMetrics {
	t.Helper()
	return metrics.NewTelemetryMetrics(prometheus.NewRegistry())
}

// testSpan builds a sampled span stub. The SDK batch processor ignores spans
// whose context is not sampled, so gate tests must use sampled contexts to
// exercise the real code path.
func testSpan(name string) sdktrace.ReadOnlySpan {
	traceID, _ := trace.TraceIDFromHex(validTraceID)
	spanID, _ := trace.SpanIDFromHex(validSpanID)
	return tracetest.SpanStub{
		Name: name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		}),
	}.Snapshot()
}

// installBridge installs the D1-approved experimental drop bridge as the global
// meter provider and enables the SDK observability flag for the duration of a
// test. These tests mutate process-global state and must run serially (no
// t.Parallel); state is restored by t.Cleanup.
func installBridge(t *testing.T, m *metrics.TelemetryMetrics, warnings *WarningDispatcher) {
	t.Helper()

	prevMP := otel.GetMeterProvider()
	prevObs, hadObs := os.LookupEnv(observabilityEnvKey)
	_ = os.Setenv(observabilityEnvKey, "true")
	otel.SetMeterProvider(NewDropObservationMeterProvider(m, warnings))

	t.Cleanup(func() {
		otel.SetMeterProvider(prevMP)
		if hadObs {
			_ = os.Setenv(observabilityEnvKey, prevObs)
		} else {
			_ = os.Unsetenv(observabilityEnvKey)
		}
	})
}

// The native batch processor must observe exact native queue-full drops and
// never block request goroutines on telemetry capacity.
func TestNativeBatchProcessorCountsDropsExactly(t *testing.T) {
	m := newTestMetrics(t)
	warnings := NewWarningDispatcher(NewWarningLimiter(time.Hour), nil)
	defer warnings.Stop()
	installBridge(t, m, warnings)

	exporter := newBlockingExporter()
	outcome := NewOutcomeExporter(exporter, m, warnings)
	processor := sdktrace.NewBatchSpanProcessor(outcome,
		sdktrace.WithMaxQueueSize(4),
		sdktrace.WithMaxExportBatchSize(4),
		sdktrace.WithBatchTimeout(time.Hour),
		sdktrace.WithExportTimeout(time.Hour),
	)

	// Fill the queue; the export worker drains all four spans into an in-flight
	// export and blocks, leaving the queue empty.
	for i := 0; i < 4; i++ {
		processor.OnEnd(testSpan("fill"))
	}
	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("exporter was never invoked")
	}

	// Refill the now-empty queue, then overflow it by exactly one span.
	for i := 0; i < 4; i++ {
		processor.OnEnd(testSpan("refill"))
	}
	processor.OnEnd(testSpan("overflow"))

	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 1 {
		t.Fatalf("dropped = %v, want exactly 1 native queue-full drop", got)
	}

	exporter.unblock()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := processor.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// OnEnd must never wait for telemetry capacity, even when the exporter blocks,
// and every completed sampled span stays accounted for exactly once.
func TestNativeBatchProcessorOnEndIsNonBlocking(t *testing.T) {
	const (
		queueSize = 8
		batchSize = 8
		total     = 200
	)

	m := newTestMetrics(t)
	warnings := NewWarningDispatcher(NewWarningLimiter(time.Hour), nil)
	defer warnings.Stop()
	installBridge(t, m, warnings)

	exporter := newBlockingExporter()
	outcome := NewOutcomeExporter(exporter, m, warnings)
	processor := sdktrace.NewBatchSpanProcessor(outcome,
		sdktrace.WithMaxQueueSize(queueSize),
		sdktrace.WithMaxExportBatchSize(batchSize),
		sdktrace.WithBatchTimeout(time.Hour),
		sdktrace.WithExportTimeout(time.Hour),
	)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			processor.OnEnd(testSpan("test"))
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("OnEnd dispatch took %v during saturation; requests must not wait", elapsed)
	}

	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("exporter was never invoked")
	}

	dropped := int(testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))))
	if dropped <= 0 {
		t.Fatalf("dropped = %d, want the native queue to saturate under %d spans", dropped, total)
	}

	exporter.unblock()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := processor.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestOutcomeExporterClassifiesAndSanitizesFailures(t *testing.T) {
	secret := "https://collector.example.test/?token=sup3r-s3cret"

	tests := []struct {
		name       string
		innerErr   error
		wantReason metrics.ExporterFailureReason
	}{
		{
			name:       "timeout",
			innerErr:   errors.Join(context.DeadlineExceeded, errors.New(secret)),
			wantReason: metrics.ExporterFailureTimeout,
		},
		{
			name:       "non-success response",
			innerErr:   errors.New("failed to send to " + secret + ": 503 Service Unavailable"),
			wantReason: metrics.ExporterFailureServerError,
		},
		{
			name:       "unreachable receiver",
			innerErr:   &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(secret)},
			wantReason: metrics.ExporterFailureTransport,
		},
		{
			name:       "cancelled",
			innerErr:   context.Canceled,
			wantReason: metrics.ExporterFailureTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMetrics(t)
			warningCh := make(chan string, 1)
			dispatcher := NewWarningDispatcher(NewWarningLimiter(time.Minute), func(message string) {
				select {
				case warningCh <- message:
				default:
				}
			})
			defer dispatcher.Stop()

			exporter := NewOutcomeExporter(&recordingExporter{err: tt.innerErr}, m, dispatcher)

			err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{testSpan("a")})
			if err == nil {
				t.Fatalf("ExportSpans() error = nil, want a sanitized failure")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error %q leaks the destination detail", err)
			}
			if strings.Contains(err.Error(), "token") {
				t.Fatalf("error %q leaks credential-ish detail", err)
			}
			if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(tt.wantReason))); got != 1 {
				t.Fatalf("failure count for %q = %v, want 1", tt.wantReason, got)
			}

			select {
			case warning := <-warningCh:
				if strings.Contains(warning, secret) || strings.Contains(warning, "token") {
					t.Fatalf("warning %q leaks destination details", warning)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("warning was not delivered")
			}
		})
	}
}

func TestOutcomeExporterWarningsAreRateLimited(t *testing.T) {
	m := newTestMetrics(t)
	limiter := NewWarningLimiter(30 * time.Second)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	warningCh := make(chan string, 8)
	dispatcher := NewWarningDispatcher(limiter, func(message string) { warningCh <- message })
	defer dispatcher.Stop()

	exporter := NewOutcomeExporter(&recordingExporter{err: context.DeadlineExceeded}, m, dispatcher)

	spans := []sdktrace.ReadOnlySpan{testSpan("a")}
	for i := 0; i < 5; i++ {
		if err := exporter.ExportSpans(context.Background(), spans); err == nil {
			t.Fatalf("ExportSpans() error = nil, want failure")
		}
	}

	if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTimeout))); got != 5 {
		t.Fatalf("failure count = %v, want all 5 attempts counted", got)
	}

	if got := collectWarnings(warningCh, 500*time.Millisecond); len(got) != 1 {
		t.Fatalf("warnings = %d, want 1 within the rate-limit window", len(got))
	}

	now = now.Add(31 * time.Second)
	if err := exporter.ExportSpans(context.Background(), spans); err == nil {
		t.Fatalf("ExportSpans() error = nil, want failure")
	}
	if got := collectWarnings(warningCh, 500*time.Millisecond); len(got) != 1 {
		t.Fatalf("warnings = %d, want 1 more after the window elapsed", len(got))
	}
}

// A slow warning sink must never block a request goroutine, and the handoff
// queue must stay bounded.
func TestWarningDispatcherIsNonBlockingWithSlowSink(t *testing.T) {
	released := make(chan struct{})
	dispatcher := NewWarningDispatcher(NewWarningLimiter(time.Minute), func(string) {
		<-released
	})
	defer func() {
		close(released)
		dispatcher.Stop()
	}()

	start := time.Now()
	for i := 0; i < warningQueueCapacity*4; i++ {
		dispatcher.Warn(warningQueueFull)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Warn blocked for %v behind a slow sink", elapsed)
	}
}

func TestWarningDispatcherDeliversToFastSink(t *testing.T) {
	delivered := make(chan string, 1)
	dispatcher := NewWarningDispatcher(NewWarningLimiter(time.Minute), func(message string) {
		select {
		case delivered <- message:
		default:
		}
	})

	dispatcher.Warn("telemetry export failed: timeout")

	select {
	case got := <-delivered:
		if got != "telemetry export failed: timeout" {
			t.Fatalf("delivered warning = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("warning was not delivered to the fast sink")
	}
	dispatcher.Stop()
}

// The drop bridge must count only queue_full additions on the batching-span
// processor counter; successful additions and unrelated instruments are no-ops.
func TestDropObservationCounterRecordsQueueFullOnly(t *testing.T) {
	m := newTestMetrics(t)
	warnings := NewWarningDispatcher(NewWarningLimiter(time.Hour), nil)
	defer warnings.Stop()

	bridge := NewDropObservationMeterProvider(m, warnings)
	meter := bridge.Meter("go.opentelemetry.io/otel/sdk/trace/internal/observ")

	counter, err := meter.Int64Counter(sdkSpanProcessedInstrument)
	if err != nil {
		t.Fatalf("Int64Counter() error = %v", err)
	}

	queueFull := metric.WithAttributes(
		attribute.String("otel.component.type", "batching_span_processor"),
		attribute.String("otel.component.name", "batching_span_processor/0"),
		attribute.String("error.type", "queue_full"),
	)
	counter.Add(context.Background(), 1, queueFull)
	counter.Add(context.Background(), 1, queueFull)

	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 2 {
		t.Fatalf("dropped = %v, want 2 queue-full drops", got)
	}

	// Successful processing carries no error.type and must be ignored.
	counter.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("otel.component.type", "batching_span_processor"),
		attribute.String("otel.component.name", "batching_span_processor/0"),
	))
	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 2 {
		t.Fatalf("dropped = %v after successful additions, want still 2", got)
	}

	// A different component with queue_full must not count.
	counter.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("otel.component.type", "simple_span_processor"),
		attribute.String("error.type", "queue_full"),
	))
	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 2 {
		t.Fatalf("dropped = %v after foreign-component addition, want still 2", got)
	}
}

// Unknown instruments on the bridge must be no-ops that never panic or expose
// new series.
func TestDropObservationUnknownInstrumentsAreNoOp(t *testing.T) {
	m := newTestMetrics(t)
	warnings := NewWarningDispatcher(NewWarningLimiter(time.Hour), nil)
	defer warnings.Stop()

	bridge := NewDropObservationMeterProvider(m, warnings)
	meter := bridge.Meter("anything")

	other, err := meter.Int64Counter("some.other.counter")
	if err != nil {
		t.Fatalf("Int64Counter(other) error = %v", err)
	}
	other.Add(context.Background(), 5)

	gauge, err := meter.Int64ObservableGauge("some.gauge")
	if err != nil {
		t.Fatalf("Int64ObservableGauge() error = %v", err)
	}
	_ = gauge

	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 0 {
		t.Fatalf("dropped = %v, want 0 for unrelated instruments", got)
	}
}

func TestWarningLimiterAllowsOncePerWindow(t *testing.T) {
	now := time.Now()
	limiter := NewWarningLimiter(10 * time.Second)
	limiter.now = func() time.Time { return now }

	if !limiter.Allow() {
		t.Fatalf("first Allow() = false, want true")
	}
	if limiter.Allow() {
		t.Fatalf("second Allow() = true, want false inside the window")
	}
	now = now.Add(11 * time.Second)
	if !limiter.Allow() {
		t.Fatalf("Allow() = false, want true after the window")
	}
}

func TestExportErrorClassificationAndSanitization(t *testing.T) {
	secret := "https://collector.example.test/v1/traces?api_key=abc123"

	classified := classifyExportError(errors.Join(context.DeadlineExceeded, errors.New(secret)))
	if classified != metrics.ExporterFailureTimeout {
		t.Fatalf("classifyExportError(deadline) = %q, want %q", classified, metrics.ExporterFailureTimeout)
	}

	sanitized := sanitizeExportError(classified)
	if sanitized == nil {
		t.Fatalf("sanitizeExportError() = nil, want an error")
	}
	if strings.Contains(sanitized.Error(), secret) || strings.Contains(sanitized.Error(), "api_key") {
		t.Fatalf("sanitized error %q leaks destination details", sanitized)
	}
	if !strings.Contains(sanitized.Error(), string(metrics.ExporterFailureTimeout)) {
		t.Fatalf("sanitized error %q should name the bounded reason", sanitized)
	}
}

// The D4 debug formatter derives diagnostics from only the bounded reason;
// secret-bearing raw errors never surface even with debug logging enabled.
func TestDebugDiagnosticNeverIncludesRawErrorText(t *testing.T) {
	secret := "https://collector.example.test/?api_key=sup3r-s3cret"
	raw := errors.New("failed to send to " + secret + ": 503 Service Unavailable")

	reason := classifyExportError(raw)
	diag := debugDiagnostic(reason)

	if strings.Contains(diag, secret) || strings.Contains(diag, "api_key") {
		t.Fatalf("diagnostic %q leaks raw error text", diag)
	}
	if !strings.Contains(diag, string(reason)) {
		t.Fatalf("diagnostic %q should name the bounded reason", diag)
	}
}

// The global error handler must never recount a marker error already classified,
// counted, and warned at the exporter boundary.
func TestExportErrorHandlerDoesNotRecountMarkerErrors(t *testing.T) {
	m := newTestMetrics(t)
	warnings := NewWarningDispatcher(NewWarningLimiter(time.Hour), nil)
	defer warnings.Stop()
	handler := NewExportErrorHandler(m, warnings)

	handler.Handle(&ExportError{Reason: metrics.ExporterFailureTimeout})
	if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTimeout))); got != 0 {
		t.Fatalf("marker error was recounted: %v", got)
	}

	handler.Handle(context.DeadlineExceeded)
	if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTimeout))); got != 1 {
		t.Fatalf("raw deadline error count = %v, want 1", got)
	}
}

// A destination outage must never block request processing, every failure is
// classified once, and repeat warnings stay rate-limited.
func TestNativeBatchProcessorExporterOutageNeverBlocksRequests(t *testing.T) {
	const (
		queueSize = 8
		total     = 300
	)

	m := newTestMetrics(t)
	limiter := NewWarningLimiter(time.Hour)
	collector := newWarningCollector()
	dispatcher := NewWarningDispatcher(limiter, collector.add)
	defer dispatcher.Stop()

	outcome := NewOutcomeExporter(&recordingExporter{err: context.DeadlineExceeded}, m, dispatcher)
	processor := sdktrace.NewBatchSpanProcessor(outcome,
		sdktrace.WithMaxQueueSize(queueSize),
		sdktrace.WithMaxExportBatchSize(queueSize),
		sdktrace.WithBatchTimeout(10*time.Millisecond),
		sdktrace.WithExportTimeout(2*time.Second),
	)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			processor.OnEnd(testSpan("test"))
		}()
	}
	wg.Wait()

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("dispatch took %v during a destination outage; requests must not wait", elapsed)
	}

	waitForFailure(t, m, metrics.ExporterFailureTimeout)

	for _, warning := range collector.snapshot() {
		if strings.Contains(warning, "collector") || strings.Contains(warning, "http") {
			t.Fatalf("warning %q leaks destination detail", warning)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := processor.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
}

// Saturation must keep background work and accepted in-memory spans bounded.
func TestNativeBatchProcessorKeepsGoroutinesBounded(t *testing.T) {
	const (
		queueSize = 8
		total     = 500
	)

	before := runtime.NumGoroutine()

	m := newTestMetrics(t)
	warnings := NewWarningDispatcher(NewWarningLimiter(time.Hour), nil)
	defer warnings.Stop()
	installBridge(t, m, warnings)

	exporter := newBlockingExporter()
	outcome := NewOutcomeExporter(exporter, m, warnings)
	processor := sdktrace.NewBatchSpanProcessor(outcome,
		sdktrace.WithMaxQueueSize(queueSize),
		sdktrace.WithMaxExportBatchSize(queueSize),
		sdktrace.WithBatchTimeout(time.Hour),
		sdktrace.WithExportTimeout(time.Hour),
	)

	for i := 0; i < total; i++ {
		processor.OnEnd(testSpan("test"))
	}

	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("exporter was never invoked")
	}

	// The batch processor owns a small, fixed worker set: growth must stay far
	// below the number of dropped spans.
	if grown := runtime.NumGoroutine() - before; grown > 25 {
		t.Fatalf("goroutines grew by %d under saturation, want bounded growth", grown)
	}

	exporter.unblock()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := processor.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// warningCollector is a race-safe warning sink for tests whose dispatcher
// worker runs concurrently with assertions.
type warningCollector struct {
	mu   sync.Mutex
	msgs []string
}

func newWarningCollector() *warningCollector { return &warningCollector{} }

func (c *warningCollector) add(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, message)
}

func (c *warningCollector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.msgs...)
}

// collectWarnings drains every warning delivered within d.
func collectWarnings(ch chan string, d time.Duration) []string {
	var out []string
	timeout := time.After(d)
	for {
		select {
		case message := <-ch:
			out = append(out, message)
		case <-timeout:
			return out
		}
	}
}

// waitForFailure waits until the given exporter-failure reason has been counted.
func waitForFailure(t *testing.T, m *metrics.TelemetryMetrics, reason metrics.ExporterFailureReason) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(reason))); got >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("exporter failure counter for %q never incremented", reason)
}

// blockingExporter parks inside ExportSpans until unblocked.
type blockingExporter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingExporter() *blockingExporter {
	return &blockingExporter{started: make(chan struct{}), release: make(chan struct{})}
}

func (e *blockingExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	e.once.Do(func() { close(e.started) })
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *blockingExporter) Shutdown(context.Context) error {
	e.unblock()
	return nil
}

func (e *blockingExporter) unblock() {
	select {
	case <-e.release:
	default:
		close(e.release)
	}
}

// recordingExporter records calls and optionally fails.
type recordingExporter struct {
	err error
}

func (e *recordingExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return e.err }

func (e *recordingExporter) Shutdown(context.Context) error { return nil }
