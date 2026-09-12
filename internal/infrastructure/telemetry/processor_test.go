package telemetry

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/infrastructure/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

// recordingProcessor forwards OnEnd calls so gating behavior is observable.
type recordingProcessor struct {
	mu       sync.Mutex
	received int
	shutdown func(context.Context) error
	flushed  int
}

func (p *recordingProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (p *recordingProcessor) OnEnd(sdktrace.ReadOnlySpan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.received++
}

func (p *recordingProcessor) Shutdown(ctx context.Context) error {
	if p.shutdown != nil {
		return p.shutdown(ctx)
	}
	return nil
}

func (p *recordingProcessor) ForceFlush(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushed++
	return nil
}

func (p *recordingProcessor) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.received
}

func TestDropProcessorDropsNewSpansWhenCapacityIsFull(t *testing.T) {
	m := newTestMetrics(t)
	next := &recordingProcessor{}
	proc := NewDropProcessor(next, DropProcessorConfig{Capacity: 2, Metrics: m})

	for i := 0; i < 5; i++ {
		proc.OnEnd(testSpan("test"))
	}

	if got := next.count(); got != 2 {
		t.Fatalf("forwarded spans = %d, want 2 (capacity)", got)
	}
	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 3 {
		t.Fatalf("dropped = %v, want 3", got)
	}
	if got := proc.Gate().InFlight(); got != 2 {
		t.Fatalf("in-flight = %d, want 2", got)
	}
}

// OnEnd must never wait for telemetry capacity, even when the exporter blocks.
func TestDropProcessorOnEndIsNonBlockingWithBlockingExporter(t *testing.T) {
	m := newTestMetrics(t)
	gate := NewTokenGate(4)
	exporter := newBlockingExporter()
	wrapper := NewCapacityExporter(exporter, gate, m, NewWarningLimiter(time.Minute))

	batch := sdktrace.NewBatchSpanProcessor(wrapper,
		sdktrace.WithMaxQueueSize(64),
		sdktrace.WithMaxExportBatchSize(4),
		sdktrace.WithBatchTimeout(50*time.Millisecond),
		sdktrace.WithExportTimeout(time.Hour),
		sdktrace.WithBlocking(),
	)
	proc := NewDropProcessor(batch, DropProcessorConfig{Capacity: 4, Metrics: m, Gate: gate})

	const total = 20
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < total; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				proc.OnEnd(testSpan("test"))
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("OnEnd blocked; request goroutines must never wait for telemetry capacity")
	}

	// The first accepted batch is now parked inside the blocking exporter.
	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("exporter was never invoked")
	}

	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != total-4 {
		t.Fatalf("dropped = %v, want %d", got, total-4)
	}

	exporter.unblock()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := proc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
}

// Capacity is released exactly once per span handed to the exporter.
func TestCapacityExporterReleasesCapacityExactlyOncePerSpan(t *testing.T) {
	m := newTestMetrics(t)
	gate := NewTokenGate(2)
	inner := &recordingExporter{}
	wrapper := NewCapacityExporter(inner, gate, m, NewWarningLimiter(time.Minute))

	if !gate.TryAcquire() || !gate.TryAcquire() {
		t.Fatalf("gate should accept up to its capacity")
	}
	if got := gate.InFlight(); got != 2 {
		t.Fatalf("in-flight = %d, want 2", got)
	}
	if gate.TryAcquire() {
		t.Fatalf("gate accepted beyond capacity")
	}

	spans := []sdktrace.ReadOnlySpan{testSpan("a"), testSpan("b")}
	if err := wrapper.ExportSpans(context.Background(), spans); err != nil {
		t.Fatalf("ExportSpans() error = %v, want nil", err)
	}
	if got := gate.InFlight(); got != 0 {
		t.Fatalf("in-flight = %d, want 0 after export", got)
	}
	if !gate.TryAcquire() {
		t.Fatalf("capacity was not returned after export")
	}

	// A single-span batch releases exactly one token.
	if err := wrapper.ExportSpans(context.Background(), spans[:1]); err != nil {
		t.Fatalf("ExportSpans() error = %v, want nil", err)
	}
	if got := gate.InFlight(); got != 0 {
		t.Fatalf("in-flight = %d, want 0 after second export", got)
	}
	if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureOtherReason))); got != 0 {
		t.Fatalf("exporter failure counter = %v, want 0", got)
	}
}

func TestCapacityExporterClassifiesAndSanitizesFailures(t *testing.T) {
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
			gate := NewTokenGate(1)
			var warnings []string
			wrapper := NewCapacityExporter(
				&recordingExporter{err: tt.innerErr},
				gate,
				m,
				NewWarningLimiter(time.Minute),
			)
			wrapper.SetWarningSink(func(message string) { warnings = append(warnings, message) })

			gate.TryAcquire()
			err := wrapper.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{testSpan("a")})
			if err == nil {
				t.Fatalf("ExportSpans() error = nil, want a sanitized failure")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error %q leaks the destination detail", err)
			}
			if strings.Contains(err.Error(), "token") {
				t.Fatalf("error %q leaks credential-ish detail", err)
			}
			if got := gate.InFlight(); got != 0 {
				t.Fatalf("in-flight = %d, want 0: capacity must be released even on failure", got)
			}
			if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(tt.wantReason))); got != 1 {
				t.Fatalf("failure count for %q = %v, want 1", tt.wantReason, got)
			}
			if len(warnings) != 1 {
				t.Fatalf("warnings = %v, want exactly one bounded warning", warnings)
			}
			if strings.Contains(warnings[0], secret) || strings.Contains(warnings[0], "token") {
				t.Fatalf("warning %q leaks destination details", warnings[0])
			}
		})
	}
}

func TestCapacityExporterWarningsAreRateLimited(t *testing.T) {
	m := newTestMetrics(t)
	gate := NewTokenGate(4)
	limiter := NewWarningLimiter(30 * time.Second)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	var warningCount int
	wrapper := NewCapacityExporter(&recordingExporter{err: context.DeadlineExceeded}, gate, m, limiter)
	wrapper.SetWarningSink(func(string) { warningCount++ })

	spans := []sdktrace.ReadOnlySpan{testSpan("a")}
	for i := 0; i < 5; i++ {
		gate.TryAcquire()
		if err := wrapper.ExportSpans(context.Background(), spans); err == nil {
			t.Fatalf("ExportSpans() error = nil, want failure")
		}
	}

	if warningCount != 1 {
		t.Fatalf("warnings = %d, want 1 within the rate-limit window", warningCount)
	}
	if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTimeout))); got != 5 {
		t.Fatalf("failure count = %v, want all 5 attempts counted", got)
	}

	now = now.Add(31 * time.Second)
	gate.TryAcquire()
	if err := wrapper.ExportSpans(context.Background(), spans); err == nil {
		t.Fatalf("ExportSpans() error = nil, want failure")
	}
	if warningCount != 2 {
		t.Fatalf("warnings = %d, want 2 after the window elapsed", warningCount)
	}
}

func TestDropProcessorDropWarningsAreRateLimited(t *testing.T) {
	m := newTestMetrics(t)
	next := &recordingProcessor{}
	now := time.Now()
	limiter := NewWarningLimiter(30 * time.Second)
	limiter.now = func() time.Time { return now }

	var warnings []string
	proc := NewDropProcessor(next, DropProcessorConfig{Capacity: 1, Metrics: m, Warnings: limiter})
	proc.SetWarningSink(func(message string) { warnings = append(warnings, message) })

	for i := 0; i < 6; i++ {
		proc.OnEnd(testSpan("test"))
	}

	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one rate-limited warning", warnings)
	}
	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 5 {
		t.Fatalf("dropped = %v, want 5 (drops stay visible even when warnings are suppressed)", got)
	}
}

func TestDropProcessorShutdownIsBoundedAndIdempotent(t *testing.T) {
	m := newTestMetrics(t)
	next := &recordingProcessor{}
	next.shutdown = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	proc := NewDropProcessor(next, DropProcessorConfig{Capacity: 2, Metrics: m})

	// Occupy capacity so shutdown must also release it.
	proc.OnEnd(testSpan("a"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	firstErr := proc.Shutdown(ctx)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("Shutdown took %v, want it bounded by the supplied deadline", elapsed)
	}
	if firstErr == nil {
		t.Fatalf("Shutdown() error = nil, want the deadline error to propagate")
	}

	// Second call must return immediately with the same result.
	start = time.Now()
	secondErr := proc.Shutdown(context.Background())
	if time.Since(start) > time.Second {
		t.Fatalf("second Shutdown blocked")
	}
	if secondErr != firstErr {
		t.Fatalf("second Shutdown() error = %v, want the recorded %v", secondErr, firstErr)
	}
	if got := proc.Gate().InFlight(); got != 0 {
		t.Fatalf("in-flight = %d, want 0 after shutdown releases capacity", got)
	}
}

func TestDropProcessorStopsIntakeAfterShutdown(t *testing.T) {
	m := newTestMetrics(t)
	next := &recordingProcessor{}
	proc := NewDropProcessor(next, DropProcessorConfig{Capacity: 2, Metrics: m})

	if err := proc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}

	proc.OnEnd(testSpan("after-shutdown"))

	if got := next.count(); got != 0 {
		t.Fatalf("forwarded spans = %d, want 0 after shutdown", got)
	}
	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))); got != 0 {
		t.Fatalf("dropped = %v, want 0: post-shutdown intake is not queue pressure", got)
	}
}

func TestDropProcessorForceFlushDelegatesAndShutdownIsIdempotent(t *testing.T) {
	m := newTestMetrics(t)
	next := &recordingProcessor{}
	proc := NewDropProcessor(next, DropProcessorConfig{Capacity: 1, Metrics: m})

	if err := proc.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush() error = %v, want nil", err)
	}
	if next.flushed != 1 {
		t.Fatalf("flush delegations = %d, want 1", next.flushed)
	}

	var shutdownCalls int
	next.shutdown = func(context.Context) error {
		shutdownCalls++
		return nil
	}
	if err := proc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
	if err := proc.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v, want nil", err)
	}
	if shutdownCalls != 1 {
		t.Fatalf("underlying shutdown calls = %d, want 1 (idempotent)", shutdownCalls)
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

// Concurrency-safe, exact drop-new accounting under contention.
func TestDropProcessorConcurrentSaturationAccountsExactly(t *testing.T) {
	const (
		capacity    = 4
		total       = 200
		dispatchers = 8
	)

	m := newTestMetrics(t)
	gate := NewTokenGate(capacity)
	exporter := newBlockingExporter()
	limiter := NewWarningLimiter(time.Hour)
	wrapper := NewCapacityExporter(exporter, gate, m, limiter)

	batch := sdktrace.NewBatchSpanProcessor(wrapper,
		sdktrace.WithMaxQueueSize(capacity*2),
		sdktrace.WithMaxExportBatchSize(capacity),
		sdktrace.WithBatchTimeout(20*time.Millisecond),
		sdktrace.WithExportTimeout(time.Hour),
		sdktrace.WithBlocking(),
	)
	forwarder := &countingForwarder{next: batch}
	proc := NewDropProcessor(forwarder, DropProcessorConfig{
		Capacity: capacity,
		Metrics:  m,
		Warnings: limiter,
		Gate:     gate,
	})

	var warnings []string
	proc.SetWarningSink(func(message string) { warnings = append(warnings, message) })

	var wg sync.WaitGroup
	perDispatcher := total / dispatchers
	for i := 0; i < dispatchers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perDispatcher; j++ {
				proc.OnEnd(testSpan("test"))
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("OnEnd blocked under saturation")
	}

	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("exporter was never invoked")
	}

	accepted := forwarder.accepted()
	dropped := int(testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))))

	if accepted != capacity {
		t.Fatalf("accepted = %d, want exactly the capacity %d while the exporter is stalled", accepted, capacity)
	}
	if accepted+dropped != perDispatcher*dispatchers {
		t.Fatalf("accepted(%d) + dropped(%d) != dispatched(%d): every span must be accounted for", accepted, dropped, perDispatcher*dispatchers)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1 rate-limited warning despite %d drops", len(warnings), dropped)
	}

	exporter.unblock()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := proc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
}

// A destination outage must never block request processing, and every completed
// span stays accounted for exactly once.
func TestDropProcessorExporterOutageNeverBlocksRequests(t *testing.T) {
	const (
		capacity = 8
		total    = 300
	)

	m := newTestMetrics(t)
	gate := NewTokenGate(capacity)
	limiter := NewWarningLimiter(time.Hour)

	var warnings []string
	wrapper := NewCapacityExporter(&recordingExporter{err: context.DeadlineExceeded}, gate, m, limiter)
	wrapper.SetWarningSink(func(message string) { warnings = append(warnings, message) })

	batch := sdktrace.NewBatchSpanProcessor(wrapper,
		sdktrace.WithMaxQueueSize(capacity*2),
		sdktrace.WithMaxExportBatchSize(capacity),
		sdktrace.WithBatchTimeout(10*time.Millisecond),
		sdktrace.WithExportTimeout(2*time.Second),
		sdktrace.WithBlocking(),
	)
	forwarder := &countingForwarder{next: batch}
	proc := NewDropProcessor(forwarder, DropProcessorConfig{
		Capacity: capacity,
		Metrics:  m,
		Warnings: limiter,
		Gate:     gate,
	})
	proc.SetWarningSink(func(message string) { warnings = append(warnings, message) })

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			proc.OnEnd(testSpan("test"))
		}()
	}
	wg.Wait()

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("dispatch took %v during a destination outage; requests must not wait", elapsed)
	}

	accepted := forwarder.accepted()
	dropped := int(testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull))))
	if accepted+dropped != total {
		t.Fatalf("accepted(%d) + dropped(%d) != dispatched(%d)", accepted, dropped, total)
	}

	if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTimeout))); got < 1 {
		t.Fatalf("exporter failure counter = %v, want the outage reported", got)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want repeat warnings rate-limited to one", len(warnings))
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "collector") || strings.Contains(warning, "http") {
			t.Fatalf("warning %q leaks destination detail", warning)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := proc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
}

// Saturation must keep background work and accepted in-memory spans bounded.
func TestDropProcessorKeepsGoroutinesAndMemoryBounded(t *testing.T) {
	const (
		capacity = 8
		total    = 500
	)

	before := runtime.NumGoroutine()

	m := newTestMetrics(t)
	gate := NewTokenGate(capacity)
	exporter := newBlockingExporter()
	wrapper := NewCapacityExporter(exporter, gate, m, NewWarningLimiter(time.Hour))

	batch := sdktrace.NewBatchSpanProcessor(wrapper,
		sdktrace.WithMaxQueueSize(capacity*2),
		sdktrace.WithMaxExportBatchSize(capacity),
		sdktrace.WithBatchTimeout(20*time.Millisecond),
		sdktrace.WithExportTimeout(time.Hour),
		sdktrace.WithBlocking(),
	)
	forwarder := &countingForwarder{next: batch}
	proc := NewDropProcessor(forwarder, DropProcessorConfig{Capacity: capacity, Metrics: m, Gate: gate})

	for i := 0; i < total; i++ {
		proc.OnEnd(testSpan("test"))
	}

	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("exporter was never invoked")
	}

	if got := forwarder.accepted(); got != capacity {
		t.Fatalf("accepted = %d, want the in-memory work bounded by capacity %d", got, capacity)
	}
	if got := gate.InFlight(); got != capacity {
		t.Fatalf("in-flight = %d, want %d", got, capacity)
	}

	// The batch processor owns a small, fixed worker set: growth must stay far
	// below the number of dropped spans.
	if grown := runtime.NumGoroutine() - before; grown > 25 {
		t.Fatalf("goroutines grew by %d under saturation, want bounded growth", grown)
	}

	exporter.unblock()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := proc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// countingForwarder counts admitted spans while forwarding to the real processor
// so accepted + dropped accounting can be asserted exactly.
type countingForwarder struct {
	next sdktrace.SpanProcessor

	mu    sync.Mutex
	spans int
}

func (p *countingForwarder) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	p.next.OnStart(ctx, span)
}

func (p *countingForwarder) OnEnd(span sdktrace.ReadOnlySpan) {
	p.mu.Lock()
	p.spans++
	p.mu.Unlock()
	p.next.OnEnd(span)
}

func (p *countingForwarder) Shutdown(ctx context.Context) error { return p.next.Shutdown(ctx) }

func (p *countingForwarder) ForceFlush(ctx context.Context) error { return p.next.ForceFlush(ctx) }

func (p *countingForwarder) accepted() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.spans
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
