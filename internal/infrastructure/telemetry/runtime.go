package telemetry

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"gin-product-service/internal/infrastructure/metrics"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Semantic-convention attribute keys used for resource identity. They are
// declared explicitly so the exported contract does not drift with semconv
// package versions.
const (
	attrServiceName           = "service.name"
	attrServiceVersion        = "service.version"
	attrDeploymentEnvironment = "deployment.environment.name"
)

// TelemetryRuntime is the explicitly composed tracing runtime. It is created in
// the startup/composition path and passed to middleware, decorators, pgx, and
// the Redis adapter; no business code resolves it from a global.
type TelemetryRuntime struct {
	// Enabled reports whether instrumentation may be attached.
	Enabled bool
	// TracerProvider is the explicit provider; a no-op provider when disabled.
	TracerProvider trace.TracerProvider
	// Propagator extracts/injects trace context (W3C plus filtered baggage).
	Propagator propagation.TextMapPropagator
	// Metrics carries the bounded drop/failure counters. Never nil.
	Metrics *metrics.TelemetryMetrics

	// provider is the concrete SDK provider that owns processors and flushing.
	provider *sdktrace.TracerProvider
	// warnings rate-limits sanitized operator warnings across the runtime.
	warnings *WarningLimiter
	// sink receives sanitized warnings (logs in production, a recorder in tests).
	sink WarningSink
	// shutdownTimeout bounds the telemetry portion of the service shutdown window.
	shutdownTimeout time.Duration

	shutdownOnce sync.Once
	shutdownErr  error
}

// RuntimeOption customises explicit runtime composition.
type RuntimeOption func(*runtimeOptions)

type runtimeOptions struct {
	metrics  *metrics.TelemetryMetrics
	sink     WarningSink
	warnings *WarningLimiter
}

// WithTelemetryMetrics supplies the bounded drop/failure collectors. When
// omitted, collectors are registered on the process default registry so the
// existing /metrics endpoint scrapes them.
func WithTelemetryMetrics(m *metrics.TelemetryMetrics) RuntimeOption {
	return func(o *runtimeOptions) { o.metrics = m }
}

// WithWarningSink receives sanitized, rate-limited operator warnings.
func WithWarningSink(sink WarningSink) RuntimeOption {
	return func(o *runtimeOptions) { o.sink = sink }
}

// WithWarningLimiter overrides the warning rate-limit window, mainly for tests.
func WithWarningLimiter(limiter *WarningLimiter) RuntimeOption {
	return func(o *runtimeOptions) { o.warnings = limiter }
}

// NewRuntime validates the supplied configuration and builds the tracing
// runtime. A disabled configuration yields a no-op runtime and performs no I/O.
// An enabled but unusable configuration fails before any request is accepted.
func NewRuntime(ctx context.Context, cfg TelemetryConfig, opts ...RuntimeOption) (*TelemetryRuntime, error) {
	options := runtimeOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	runtimeMetrics := options.metrics
	if runtimeMetrics == nil {
		runtimeMetrics = metrics.NewTelemetryMetrics(nil)
	}
	warnings := options.warnings
	if warnings == nil {
		warnings = NewWarningLimiter(DefaultWarningWindow)
	}

	if !cfg.Enabled {
		return &TelemetryRuntime{
			Enabled:         false,
			TracerProvider:  trace.NewNoopTracerProvider(),
			Propagator:      propagation.NewCompositeTextMapPropagator(),
			Metrics:         runtimeMetrics,
			warnings:        warnings,
			sink:            options.sink,
			shutdownTimeout: cfg.ShutdownTimeout,
		}, nil
	}

	// Re-validate so a programmatically built configuration cannot bypass the
	// startup gate.
	if err := validateEnabled(cfg); err != nil {
		return nil, err
	}

	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}

	exporter, err := newOTLPHTTPExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("build otlp/http trace exporter: %w", err)
	}

	gate := NewTokenGate(cfg.QueueSize)
	capacityExporter := NewCapacityExporter(exporter, gate, runtimeMetrics, warnings)
	capacityExporter.SetWarningSink(options.sink)

	processor := sdktrace.NewBatchSpanProcessor(capacityExporter,
		// The SDK queue never silently drops: the bounded gate in front of it owns
		// drop-new behavior, and it admits fewer spans than the queue can hold.
		sdktrace.WithMaxQueueSize(cfg.QueueSize),
		sdktrace.WithMaxExportBatchSize(cfg.BatchSize),
		sdktrace.WithBatchTimeout(cfg.ScheduleDelay),
		sdktrace.WithExportTimeout(cfg.BatchExportTimeout),
		sdktrace.WithBlocking(),
	)

	dropProcessor := NewDropProcessor(processor, DropProcessorConfig{
		Capacity: cfg.QueueSize,
		Metrics:  runtimeMetrics,
		Warnings: warnings,
		Gate:     gate,
	})
	dropProcessor.SetWarningSink(options.sink)

	propagator := NewPropagator(cfg.BaggageAllowlist)

	// ---- my custom config -----------------

	// exporter, errN := stdouttrace.New(
	// 	stdouttrace.WithWriter(os.Stdout),
	// 	stdouttrace.WithPrettyPrint(),
	// )
	// if errN != nil {
	// 	return nil, fmt.Errorf("failed to create stdout exporter: %w", err)
	// }

	// --------------------------------------

	provider := sdktrace.NewTracerProvider(
		// Parent-based sampling honors valid upstream decisions; the configured
		// ratio applies only when this service starts a new root trace.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.RootSampleRatio))),

		// my custom config
		// sdktrace.WithBatcher(exporter),

		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(dropProcessor),
	)

	// Global registration exists only for third-party library compatibility;
	// every feature-owned component above receives the runtime explicitly.
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagator)
	otel.SetErrorHandler(NewExportErrorHandler(runtimeMetrics, warnings, options.sink))

	return &TelemetryRuntime{
		Enabled:         true,
		TracerProvider:  provider,
		Propagator:      propagator,
		Metrics:         runtimeMetrics,
		provider:        provider,
		warnings:        warnings,
		sink:            options.sink,
		shutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}

// Provider exposes the concrete SDK provider for adapters that need to attach
// instrumentation explicitly. It is nil when tracing is disabled.
func (r *TelemetryRuntime) Provider() *sdktrace.TracerProvider {
	return r.provider
}

// Shutdown stops trace intake, makes one bounded attempt to deliver accepted
// telemetry, and returns. It is idempotent and never exceeds the configured
// telemetry shutdown timeout or the caller's deadline.
func (r *TelemetryRuntime) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		if r.provider == nil {
			r.shutdownErr = nil
			return
		}
		timeout := r.shutdownTimeout
		if timeout <= 0 {
			timeout = fallbackShutdownTimeout
		}

		shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		r.shutdownErr = r.provider.Shutdown(shutdownCtx)
		if err := ctx.Err(); err != nil && r.shutdownErr == nil {
			r.shutdownErr = err
		}
	})
	return r.shutdownErr
}

// newResource builds the exported resource identity.
func newResource(ctx context.Context, cfg TelemetryConfig) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String(attrServiceName, cfg.ServiceName),
		attribute.String(attrDeploymentEnvironment, cfg.Environment),
	}
	if version := buildVersion(); version != "" {
		attrs = append(attrs, attribute.String(attrServiceVersion, version))
	}

	return resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
	)
}

// newOTLPHTTPExporter constructs the OTLP/HTTP exporter. Construction performs
// no network I/O, so the service stays fail-open when the destination is down.
func newOTLPHTTPExporter(ctx context.Context, cfg TelemetryConfig) (sdktrace.SpanExporter, error) {
	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
		otlptracehttp.WithTimeout(cfg.ExporterTimeout),
		// Bound retries explicitly so a slow or flapping destination can never
		// extend an export, or shutdown, beyond the configured timeout.
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
			Enabled:         true,
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     cfg.ExporterTimeout / 2,
			MaxElapsedTime:  cfg.ExporterTimeout,
		}),
	}
	if cfg.Compression == CompressionNone {
		options = append(options, otlptracehttp.WithCompression(otlptracehttp.NoCompression))
	} else {
		options = append(options, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	}
	if len(cfg.Headers) > 0 {
		options = append(options, otlptracehttp.WithHeaders(cfg.Headers))
	}
	return otlptracehttp.New(ctx, options...)
}

// buildVersion reports the build version when the toolchain supplies one.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return setting.Value
		}
	}
	return ""
}

// validateEnabled enforces the enabled-configuration invariants again at runtime
// construction so programmatic callers cannot bypass the startup gate.
func validateEnabled(cfg TelemetryConfig) error {
	if cfg.ServiceName == "" {
		return &ConfigError{Field: FieldServiceName, Reason: "must not be empty"}
	}
	if cfg.Environment == "" {
		return &ConfigError{Field: FieldEnvironment, Reason: "is required when tracing is enabled"}
	}
	if err := validateEndpoint(cfg.Endpoint); err != nil {
		return err
	}
	if cfg.RootSampleRatio < 0 || cfg.RootSampleRatio > 1 {
		return &ConfigError{Field: FieldRootSampleRatio, Reason: "must be a decimal between 0 and 1"}
	}
	if cfg.QueueSize < 1 {
		return &ConfigError{Field: FieldQueueSize, Reason: "must be positive"}
	}
	if cfg.BatchSize < 1 || cfg.BatchSize > cfg.QueueSize {
		return &ConfigError{Field: FieldBatchSize, Reason: "must be positive and no larger than the queue size"}
	}
	if cfg.ExporterTimeout <= 0 || cfg.ExporterTimeout > ServiceShutdownBudget {
		return &ConfigError{Field: FieldExporterTimeout, Reason: "must be positive and within the service shutdown budget"}
	}
	if cfg.ShutdownTimeout <= 0 || cfg.ShutdownTimeout >= ServiceShutdownBudget {
		return &ConfigError{Field: FieldShutdownTimeout, Reason: "must be positive and strictly within the service shutdown budget"}
	}
	return nil
}

// ExportErrorHandler is the global OpenTelemetry error handler. It classifies,
// counts, and rate-limits SDK-surfaced failures without ever echoing raw library
// text, so endpoints, response bodies, headers, and credentials cannot leak.
type ExportErrorHandler struct {
	metrics  *metrics.TelemetryMetrics
	warnings *WarningLimiter
	sink     WarningSink
}

// NewExportErrorHandler builds the sanitized global error handler.
func NewExportErrorHandler(
	telemetryMetrics *metrics.TelemetryMetrics,
	warnings *WarningLimiter,
	sink WarningSink,
) *ExportErrorHandler {
	if warnings == nil {
		warnings = NewWarningLimiter(DefaultWarningWindow)
	}
	return &ExportErrorHandler{metrics: telemetryMetrics, warnings: warnings, sink: sink}
}

// Handle implements otel.ErrorHandler.
func (h *ExportErrorHandler) Handle(err error) {
	if err == nil {
		return
	}
	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		// Already classified, counted, and warned at the exporter boundary.
		return
	}

	reason := classifyExportError(err)
	h.metrics.RecordExporterFailure(reason)
	if h.warnings.Allow() && h.sink != nil {
		h.sink(warningExportFailedPrefix + string(reason))
	}
}

// fallbackShutdownTimeout guards programmatic construction that omits the field.
const fallbackShutdownTimeout = 5 * time.Second
