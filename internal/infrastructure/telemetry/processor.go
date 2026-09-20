package telemetry

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"gin-product-service/internal/infrastructure/metrics"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// DefaultWarningWindow bounds how often a repeated telemetry warning is emitted.
const DefaultWarningWindow = time.Minute

// warningQueueCapacity bounds the non-blocking handoff queue so a stalled log
// sink can never accumulate unbounded work.
const warningQueueCapacity = 64

// Constant, sanitized warning messages. They never include endpoints, headers,
// credentials, payloads, or raw library error text.
const (
	warningQueueFull          = "telemetry spans dropped: export queue is full"
	warningExportFailedPrefix = "telemetry export failed: "
)

// sdkSpanProcessedInstrument is the experimental SDK observability counter that
// reports finished span processing, including native queue drops. This name is
// pinned to SDK 1.46.0 and is the single interception point for the drop bridge.
const sdkSpanProcessedInstrument = "otel.sdk.processor.span.processed"

// Bounded, sanitized attribute keys and values for the SDK observability
// counter. They are declared as literals so the bridge does not import SDK
// internal packages or semconv.
const (
	attrErrorType         = attribute.Key("error.type")
	attrComponentType     = attribute.Key("otel.component.type")
	errorTypeQueueFull    = "queue_full"
	componentTypeBatching = "batching_span_processor"
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

// WarningDispatcher owns the shared rate limiter and delivers accepted warnings
// through a single worker so a slow sink can never block a request goroutine.
// The rate-limit decision is synchronous and cheap; only sink delivery is
// handed off through a bounded queue.
type WarningDispatcher struct {
	limiter *WarningLimiter
	sink    WarningSink
	queue   chan string
	done    chan struct{}
	drained chan struct{}

	stopOnce sync.Once
}

// NewWarningDispatcher builds a dispatcher sharing one limiter across the whole
// runtime and starts its single delivery worker.
func NewWarningDispatcher(limiter *WarningLimiter, sink WarningSink) *WarningDispatcher {
	if limiter == nil {
		limiter = NewWarningLimiter(DefaultWarningWindow)
	}
	d := &WarningDispatcher{
		limiter: limiter,
		sink:    sink,
		queue:   make(chan string, warningQueueCapacity),
		done:    make(chan struct{}),
		drained: make(chan struct{}),
	}
	go d.run()
	return d
}

func (d *WarningDispatcher) run() {
	defer close(d.drained)
	for {
		select {
		case msg := <-d.queue:
			d.deliver(msg)
		case <-d.done:
			// Drain already-accepted warnings before exiting so a fast sink
			// still observes them; a slow sink bounds shutdown below.
			for {
				select {
				case msg := <-d.queue:
					d.deliver(msg)
				default:
					return
				}
			}
		}
	}
}

func (d *WarningDispatcher) deliver(message string) {
	if d.sink != nil {
		d.sink(message)
	}
}

// Warn rate-limits and delivers a sanitized warning without ever blocking the
// caller on a slow sink. When the bounded queue is full the warning is dropped;
// metric accounting is unaffected.
func (d *WarningDispatcher) Warn(message string) {
	if d == nil || !d.limiter.Allow() {
		return
	}
	select {
	case d.queue <- message:
	default:
	}
}

// Stop shuts the worker down and waits, bounded, for already-queued warnings to
// reach a fast sink. It never waits indefinitely for a slow sink.
func (d *WarningDispatcher) Stop() {
	if d == nil {
		return
	}
	d.stopOnce.Do(func() {
		close(d.done)
		select {
		case <-d.drained:
		case <-time.After(time.Second):
		}
	})
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

// debugDiagnostic derives the debug-level export diagnostic from only the
// bounded reason. Unknown or untrusted raw error text is never included, so
// endpoints, headers, response bodies, and credentials cannot leak even with
// debug logging enabled.
func debugDiagnostic(reason metrics.ExporterFailureReason) string {
	return warningExportFailedPrefix + string(reason)
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

// OutcomeExporter wraps the real span exporter so each failed batch export is
// classified once, counted, warned, and returned as a sanitized marker error.
// The global error handler recognizes the marker and never recounts it.
type OutcomeExporter struct {
	inner    sdktrace.SpanExporter
	metrics  *metrics.TelemetryMetrics
	warnings *WarningDispatcher
}

var _ sdktrace.SpanExporter = (*OutcomeExporter)(nil)

// NewOutcomeExporter builds the exporter outcome wrapper.
func NewOutcomeExporter(
	inner sdktrace.SpanExporter,
	telemetryMetrics *metrics.TelemetryMetrics,
	warnings *WarningDispatcher,
) *OutcomeExporter {
	return &OutcomeExporter{inner: inner, metrics: telemetryMetrics, warnings: warnings}
}

// ExportSpans exports the batch and, on failure, counts and warns exactly once
// before returning the sanitized marker error.
func (e *OutcomeExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.inner.ExportSpans(ctx, spans)
	if err == nil {
		return nil
	}

	reason := classifyExportError(err)
	e.metrics.RecordExporterFailure(reason)
	e.warnings.Warn(debugDiagnostic(reason))
	return sanitizeExportError(reason)
}

// Shutdown closes the wrapped exporter.
func (e *OutcomeExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

// DropObservationMeterProvider bridges the SDK's experimental observability
// metric stream to the existing Prometheus drop counter. It implements only the
// public metric API by embedding no-op implementations: no SDK internal package,
// metrics SDK, reader, or exporter is introduced, and no new series is exposed.
type DropObservationMeterProvider struct {
	noop.MeterProvider
	metrics  *metrics.TelemetryMetrics
	warnings *WarningDispatcher
}

var _ metric.MeterProvider = (*DropObservationMeterProvider)(nil)

// NewDropObservationMeterProvider builds the drop bridge. Unknown meters and
// instruments are no-ops; only the SDK processed-span counter is intercepted.
func NewDropObservationMeterProvider(
	telemetryMetrics *metrics.TelemetryMetrics,
	warnings *WarningDispatcher,
) *DropObservationMeterProvider {
	return &DropObservationMeterProvider{metrics: telemetryMetrics, warnings: warnings}
}

// Meter returns a bridging meter; every scope is served, but only the pinned
// processed-span counter has observable behavior.
func (p *DropObservationMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return dropObservationMeter{provider: p}
}

type dropObservationMeter struct {
	noop.Meter
	provider *DropObservationMeterProvider
}

// Int64Counter intercepts the SDK processed-span counter and bridges its
// queue_full additions to the existing Prometheus drop counter. All other
// instruments fall through to the no-op implementation.
func (m dropObservationMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if name == sdkSpanProcessedInstrument {
		return &dropObservationCounter{metrics: m.provider.metrics, warnings: m.provider.warnings}, nil
	}
	return m.Meter.Int64Counter(name, opts...)
}

type dropObservationCounter struct {
	noop.Int64Counter
	metrics  *metrics.TelemetryMetrics
	warnings *WarningDispatcher
}

// Add observes each native processed-span addition. Only additions tagged with
// the queue_full error and the batching-span-processor component are counted as
// drops, once per dropped span, on the existing Prometheus counter. Successful
// processed-span additions and unrelated instruments are ignored.
func (c *dropObservationCounter) Add(ctx context.Context, incr int64, opts ...metric.AddOption) {
	if c == nil {
		return
	}
	attrs := metric.NewAddConfig(opts).Attributes()

	if v, ok := attrs.Value(attrErrorType); !ok || v.AsString() != errorTypeQueueFull {
		return
	}
	if v, ok := attrs.Value(attrComponentType); !ok || v.AsString() != componentTypeBatching {
		return
	}

	for i := int64(0); i < incr; i++ {
		c.metrics.RecordSpanDrop(metrics.DropReasonQueueFull)
	}
	c.warnings.Warn(warningQueueFull)
}
