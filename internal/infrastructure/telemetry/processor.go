package telemetry

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gin-product-service/internal/infrastructure/metrics"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// DefaultWarningWindow bounds how often a repeated telemetry warning is emitted.
const DefaultWarningWindow = time.Minute

// Constant, sanitized warning messages. They never include endpoints, headers,
// credentials, payloads, or raw library error text.
const (
	warningQueueFull          = "telemetry spans dropped: export queue is full"
	warningExportFailedPrefix = "telemetry export failed: "
)

// WarningSink receives already-sanitized warning messages.
type WarningSink func(message string)

// WarningLimiter emits at most one warning per window. Drops and failures are
// always counted in Prometheus; only the human-readable warning is suppressed.
type WarningLimiter struct {
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	last time.Time
}

// NewWarningLimiter builds a limiter for the supplied window.
func NewWarningLimiter(window time.Duration) *WarningLimiter {
	if window <= 0 {
		window = DefaultWarningWindow
	}
	return &WarningLimiter{window: window, now: time.Now}
}

// Allow reports whether a warning may be emitted now.
func (w *WarningLimiter) Allow() bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	current := w.now()
	if w.last.IsZero() || current.Sub(w.last) >= w.window {
		w.last = current
		return true
	}
	return false
}

// TokenGate is the bounded, non-blocking capacity gate that guarantees request
// goroutines never wait for telemetry capacity.
type TokenGate struct {
	tokens   chan struct{}
	capacity int
}

// NewTokenGate builds a gate that admits at most capacity concurrent spans.
func NewTokenGate(capacity int) *TokenGate {
	if capacity < 1 {
		capacity = 1
	}
	return &TokenGate{tokens: make(chan struct{}, capacity), capacity: capacity}
}

// Capacity is the maximum number of in-flight accepted spans.
func (g *TokenGate) Capacity() int { return g.capacity }

// InFlight reports how many accepted spans are awaiting export.
func (g *TokenGate) InFlight() int { return len(g.tokens) }

// TryAcquire admits one span without ever blocking. It reports false when full.
func (g *TokenGate) TryAcquire() bool {
	select {
	case g.tokens <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release returns exactly n tokens, stopping early if the gate is already empty.
func (g *TokenGate) Release(n int) {
	for i := 0; i < n; i++ {
		select {
		case <-g.tokens:
		default:
			return
		}
	}
}

// Reset drops all held capacity, used once intake has permanently stopped.
func (g *TokenGate) Reset() {
	for {
		select {
		case <-g.tokens:
		default:
			return
		}
	}
}

// ExportError is a sanitized export failure carrying only a bounded reason, so
// destination URLs, response bodies, headers, and credentials never surface.
type ExportError struct {
	Reason metrics.ExporterFailureReason
}

func (e *ExportError) Error() string {
	return "trace export failed (" + string(e.Reason) + ")"
}

// sanitizeExportError converts a bounded reason into the error returned upward.
func sanitizeExportError(reason metrics.ExporterFailureReason) error {
	return &ExportError{Reason: reason}
}

// classifyExportError maps a raw exporter failure onto the bounded reason
// vocabulary using error identity and type only; the message is never parsed.
func classifyExportError(err error) metrics.ExporterFailureReason {
	if err == nil {
		return metrics.ExporterFailureUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return metrics.ExporterFailureTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return metrics.ExporterFailureTransport
	}
	// Remaining exporter failures are non-success destination responses.
	return metrics.ExporterFailureServerError
}

// CapacityExporter wraps the real span exporter so capacity is released exactly
// once per span handed to the exporter and failures are classified, counted, and
// sanitized.
type CapacityExporter struct {
	inner    sdktrace.SpanExporter
	gate     *TokenGate
	metrics  *metrics.TelemetryMetrics
	warnings *WarningLimiter

	sinkMu sync.Mutex
	sink   WarningSink
}

var (
	_ sdktrace.SpanExporter  = (*CapacityExporter)(nil)
	_ sdktrace.SpanProcessor = (*DropProcessor)(nil)
)

// NewCapacityExporter builds the exporter wrapper sharing gate with the gate.
func NewCapacityExporter(
	inner sdktrace.SpanExporter,
	gate *TokenGate,
	telemetryMetrics *metrics.TelemetryMetrics,
	warnings *WarningLimiter,
) *CapacityExporter {
	if warnings == nil {
		warnings = NewWarningLimiter(DefaultWarningWindow)
	}
	return &CapacityExporter{
		inner:    inner,
		gate:     gate,
		metrics:  telemetryMetrics,
		warnings: warnings,
	}
}

// SetWarningSink installs the sanitized warning destination.
func (e *CapacityExporter) SetWarningSink(sink WarningSink) {
	e.sinkMu.Lock()
	defer e.sinkMu.Unlock()
	e.sink = sink
}

// ExportSpans exports the batch, then releases one capacity token per span
// regardless of success, so a failing destination can never strand capacity.
func (e *CapacityExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.inner.ExportSpans(ctx, spans)

	if e.gate != nil {
		e.gate.Release(len(spans))
	}
	if err == nil {
		return nil
	}

	reason := classifyExportError(err)
	e.metrics.RecordExporterFailure(reason)
	if e.warnings.Allow() {
		e.warn(warningExportFailedPrefix + string(reason))
	}
	return sanitizeExportError(reason)
}

// Shutdown closes the wrapped exporter.
func (e *CapacityExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

func (e *CapacityExporter) warn(message string) {
	e.sinkMu.Lock()
	sink := e.sink
	e.sinkMu.Unlock()
	if sink != nil {
		sink(message)
	}
}

// DropProcessorConfig configures the bounded gate in front of the SDK batch
// processor.
type DropProcessorConfig struct {
	// Capacity is the maximum number of accepted, not-yet-exported spans.
	Capacity int
	// Metrics receives bounded drop and failure counters. Optional.
	Metrics *metrics.TelemetryMetrics
	// Warnings rate-limits sanitized warnings. Optional.
	Warnings *WarningLimiter
	// Gate optionally reuses an existing gate; otherwise one is created.
	Gate *TokenGate
}

// DropProcessor implements drop-new back pressure: when the bounded gate is
// full, the newly completed span is dropped and counted instead of blocking the
// request goroutine. Accepted spans are handed to the SDK batch processor, which
// is configured to never drop silently.
type DropProcessor struct {
	next     sdktrace.SpanProcessor
	gate     *TokenGate
	metrics  *metrics.TelemetryMetrics
	warnings *WarningLimiter

	stopped atomic.Bool

	shutdownOnce sync.Once
	shutdownErr  error

	sinkMu sync.Mutex
	sink   WarningSink
}

// NewDropProcessor wraps next with the bounded capacity gate.
func NewDropProcessor(next sdktrace.SpanProcessor, cfg DropProcessorConfig) *DropProcessor {
	gate := cfg.Gate
	if gate == nil {
		gate = NewTokenGate(cfg.Capacity)
	}
	warnings := cfg.Warnings
	if warnings == nil {
		warnings = NewWarningLimiter(DefaultWarningWindow)
	}
	return &DropProcessor{
		next:     next,
		gate:     gate,
		metrics:  cfg.Metrics,
		warnings: warnings,
	}
}

// Gate exposes the shared capacity gate so the exporter wrapper can release it.
func (p *DropProcessor) Gate() *TokenGate { return p.gate }

// SetWarningSink installs the sanitized warning destination.
func (p *DropProcessor) SetWarningSink(sink WarningSink) {
	p.sinkMu.Lock()
	defer p.sinkMu.Unlock()
	p.sink = sink
}

// OnStart forwards span starts to the wrapped processor.
func (p *DropProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	p.next.OnStart(ctx, span)
}

// OnEnd admits or drops the completed span without ever blocking.
func (p *DropProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if p.stopped.Load() {
		// Intake has stopped: accepted telemetry is draining, new work is ignored.
		return
	}
	if !p.gate.TryAcquire() {
		p.metrics.RecordSpanDrop(metrics.DropReasonQueueFull)
		if p.warnings.Allow() {
			p.warn(warningQueueFull)
		}
		return
	}
	p.next.OnEnd(span)
}

// Shutdown stops intake, drains accepted telemetry within ctx, and releases all
// capacity. It is idempotent and always bounded by ctx.
func (p *DropProcessor) Shutdown(ctx context.Context) error {
	p.shutdownOnce.Do(func() {
		p.stopped.Store(true)
		p.shutdownErr = p.next.Shutdown(ctx)
		p.gate.Reset()
	})
	return p.shutdownErr
}

// ForceFlush delegates a bounded flush of accepted telemetry.
func (p *DropProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}

func (p *DropProcessor) warn(message string) {
	p.sinkMu.Lock()
	sink := p.sink
	p.sinkMu.Unlock()
	if sink != nil {
		sink(message)
	}
}
