package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/metrics"
	"gin-product-service/internal/lifecycle"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Semantic-convention attribute keys used for resource identity. They are
// declared explicitly so the exported contract does not drift with semconv
// package versions.
const (
	attrServiceName           = "service.name"
	attrServiceVersion        = "service.version"
	attrDeploymentEnvironment = "deployment.environment.name"
)

// observabilityEnvKey enables the SDK's experimental observability feature,
// which the drop bridge observes. It is set only at enabled-tracing bootstrap
// and restored to its previous value at shutdown.
const observabilityEnvKey = "OTEL_GO_X_OBSERVABILITY"

// Native sampling environment keys, bootstrapped at enabled-tracing startup so
// the SDK's parent-based ratio sampler interprets the documented 0.10 default.
const (
	envSampler    = "OTEL_TRACES_SAMPLER"
	envSamplerArg = "OTEL_TRACES_SAMPLER_ARG"
)

// Documented absent-setting defaults preserved through small selection only
// when no effective SDK setting exists.
const (
	defaultExporterTimeout    = 5000 * time.Millisecond
	defaultBatchExportTimeout = 5000 * time.Millisecond
)

// SDK tuning environment keys whose presence suppresses the documented defaults.
const (
	envExporterTimeoutTrace = "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"
	envExporterTimeout      = "OTEL_EXPORTER_OTLP_TIMEOUT"
	envCompressionTrace     = "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION"
	envCompression          = "OTEL_EXPORTER_OTLP_COMPRESSION"
	envBSPExportTimeout     = "OTEL_BSP_EXPORT_TIMEOUT"
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
	// warnings delivers sanitized, rate-limited operator warnings.
	warnings *WarningDispatcher
	// shutdownTimeout bounds the telemetry portion of the service shutdown window.
	shutdownTimeout time.Duration

	// Process-global state owned at enabled bootstrap and restored at shutdown.
	prevMeterProvider metric.MeterProvider
	prevObservability string
	hadObservability  bool
	prevSampler       string
	hadSampler        bool
	changedSampler    bool
	prevSamplerArg    string
	hadSamplerArg     bool
	changedSamplerArg bool

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
	sink := options.sink
	if sink == nil {
		// Production: route already-sanitized, rate-limited warnings to the
		// structured logger so operators still see them.
		sink = func(message string) {
			logger.L(context.Background()).Warn(message)
		}
	}
	warnings := NewWarningDispatcher(options.warnings, sink)

	if !cfg.Enabled {
		return &TelemetryRuntime{
			Enabled:         false,
			TracerProvider:  noop.NewTracerProvider(),
			Propagator:      propagation.NewCompositeTextMapPropagator(),
			Metrics:         runtimeMetrics,
			warnings:        warnings,
			shutdownTimeout: cfg.ShutdownTimeout,
		}, nil
	}

	// Re-validate so a programmatically built configuration cannot bypass the
	// startup gate.
	if err := validateEnabled(cfg); err != nil {
		warnings.Stop()
		return nil, err
	}

	// Install sanitized SDK diagnostics before any parser runs: the error
	// handler classifies without echoing raw text, and the logger is dropped so
	// native parsing diagnostics (which can contain raw header values) never
	// surface. Parsing diagnostics are never counted as export failures.
	otel.SetLogger(logr.Discard())
	otel.SetErrorHandler(NewExportErrorHandler(runtimeMetrics, warnings))

	// D3: bootstrap native parent-based sampling before the SDK constructs the
	// tracer provider. Select parentbased_traceidratio and default the missing
	// ratio argument without parsing it; explicit operator values win and the
	// previous values are restored at shutdown.
	prevSampler, hadSampler := os.LookupEnv(envSampler)
	prevSamplerArg, hadSamplerArg := os.LookupEnv(envSamplerArg)
	changedSampler := os.Getenv(envSampler) == ""
	changedSamplerArg := os.Getenv(envSamplerArg) == ""
	if changedSampler {
		_ = os.Setenv(envSampler, "parentbased_traceidratio")
	}
	if changedSamplerArg {
		_ = os.Setenv(envSamplerArg, "0.10")
	}

	res, err := newResource(ctx, cfg)
	if err != nil {
		warnings.Stop()
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}

	exporter, err := newOTLPHTTPExporter(ctx, cfg)
	if err != nil {
		warnings.Stop()
		return nil, fmt.Errorf("build otlp/http trace exporter: %w", err)
	}

	outcomeExporter := NewOutcomeExporter(exporter, runtimeMetrics, warnings)

	// The experimental drop-observation bridge must be installed before the
	// batch processor is constructed so the SDK's observability instruments
	// route to the existing Prometheus drop counter. The previous global meter
	// provider and feature flag are restored at shutdown.
	prevMeterProvider := otel.GetMeterProvider()
	prevObservability, hadObservability := os.LookupEnv(observabilityEnvKey)
	_ = os.Setenv(observabilityEnvKey, "true")
	otel.SetMeterProvider(NewDropObservationMeterProvider(runtimeMetrics, warnings))

	// Native non-blocking batching: queue/batch sizes, schedule delay, and
	// export timeout are delegated to the SDK's environment parsing. Only the
	// documented 5-second BSP export timeout is defaulted when no effective SDK
	// setting exists.
	processorOpts := make([]sdktrace.BatchSpanProcessorOption, 0, 1)
	if _, ok := os.LookupEnv(envBSPExportTimeout); !ok {
		processorOpts = append(processorOpts, sdktrace.WithExportTimeout(defaultBatchExportTimeout))
	}
	processor := sdktrace.NewBatchSpanProcessor(outcomeExporter, processorOpts...)

	propagator := NewPropagator(cfg.BaggageAllowlist)

	// Sampling is delegated to the SDK's native environment parsing, which the
	// bootstrap above pinned to parent-based ratio sampling with the 0.10
	// default.
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(processor),
	)

	// Global registration exists only for third-party library compatibility;
	// every feature-owned component above receives the runtime explicitly.
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagator)

	return &TelemetryRuntime{
		Enabled:           true,
		TracerProvider:    provider,
		Propagator:        propagator,
		Metrics:           runtimeMetrics,
		provider:          provider,
		warnings:          warnings,
		shutdownTimeout:   cfg.ShutdownTimeout,
		prevMeterProvider: prevMeterProvider,
		prevObservability: prevObservability,
		hadObservability:  hadObservability,
		prevSampler:       prevSampler,
		hadSampler:        hadSampler,
		changedSampler:    changedSampler,
		prevSamplerArg:    prevSamplerArg,
		hadSamplerArg:     hadSamplerArg,
		changedSamplerArg: changedSamplerArg,
	}, nil
}

// Provider exposes the concrete SDK provider for adapters that need to attach
// instrumentation explicitly. It is nil when tracing is disabled.
func (r *TelemetryRuntime) Provider() *sdktrace.TracerProvider {
	return r.provider
}

// EffectiveShutdownTimeout reports the telemetry delivery window the server
// reserves during shutdown so a slow HTTP drain cannot starve the flush. It is
// zero when tracing is disabled because disabled telemetry accepts nothing and
// needs no reservation.
func (r *TelemetryRuntime) EffectiveShutdownTimeout() time.Duration {
	if !r.Enabled {
		return 0
	}
	return r.shutdownTimeout
}

// Shutdown stops trace intake, makes one bounded attempt to deliver accepted
// telemetry, and returns. It is idempotent and never exceeds the configured
// telemetry shutdown timeout or the caller's deadline.
func (r *TelemetryRuntime) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		defer func() {
			r.warnings.Stop()
			r.restoreDropObservation()
		}()
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

// restoreDropObservation restores the process-global meter provider and the SDK
// observability feature flag, plus the sampling bootstrap, to the values
// captured at enabled bootstrap. It is a no-op for a disabled runtime, which
// never installed the bridge.
func (r *TelemetryRuntime) restoreDropObservation() {
	if r.prevMeterProvider == nil {
		return
	}
	otel.SetMeterProvider(r.prevMeterProvider)
	if r.hadObservability {
		_ = os.Setenv(observabilityEnvKey, r.prevObservability)
	} else {
		_ = os.Unsetenv(observabilityEnvKey)
	}
	if r.changedSampler {
		if r.hadSampler {
			_ = os.Setenv(envSampler, r.prevSampler)
		} else {
			_ = os.Unsetenv(envSampler)
		}
	}
	if r.changedSamplerArg {
		if r.hadSamplerArg {
			_ = os.Setenv(envSamplerArg, r.prevSamplerArg)
		} else {
			_ = os.Unsetenv(envSamplerArg)
		}
	}
}

// newResource builds the exported resource identity. The SDK merges its own
// environment-detected resource on top, so an explicit OTEL_SERVICE_NAME still
// overrides the documented gin-product-service fallback.
func newResource(ctx context.Context, cfg TelemetryConfig) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String(attrServiceName, DefaultServiceName),
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
// Headers, compression, and timeout are delegated to the SDK's environment
// parsing; only the documented gzip and 5-second timeout defaults are applied
// when no effective SDK setting exists.
func newOTLPHTTPExporter(ctx context.Context, cfg TelemetryConfig) (sdktrace.SpanExporter, error) {
	timeout := effectiveExporterTimeout()

	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
	}

	if !exporterTimeoutConfigured() {
		options = append(options, otlptracehttp.WithTimeout(timeout))
	}
	if !compressionConfigured() {
		options = append(options, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	}

	// Bound retries explicitly so a slow or flapping destination can never
	// extend an export, or shutdown, beyond the effective timeout.
	options = append(options, otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
		Enabled:         true,
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     timeout / 2,
		MaxElapsedTime:  timeout,
	}))

	return otlptracehttp.New(ctx, options...)
}

// effectiveExporterTimeout reports the exporter timeout in effect: the trace
// specific SDK setting, else the generic SDK setting, else the documented
// 5-second default.
func effectiveExporterTimeout() time.Duration {
	for _, key := range []string{envExporterTimeoutTrace, envExporterTimeout} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
				return time.Duration(ms) * time.Millisecond
			}
		}
	}
	return defaultExporterTimeout
}

// exporterTimeoutConfigured reports whether any effective SDK timeout setting
// exists, so the documented default is only applied when absent.
func exporterTimeoutConfigured() bool {
	return os.Getenv(envExporterTimeoutTrace) != "" || os.Getenv(envExporterTimeout) != ""
}

// compressionConfigured reports whether any effective SDK compression setting
// exists, so the documented gzip default is only applied when absent.
func compressionConfigured() bool {
	return os.Getenv(envCompressionTrace) != "" || os.Getenv(envCompression) != ""
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
	if cfg.Environment == "" {
		return &ConfigError{Field: FieldEnvironment, Reason: "is required when tracing is enabled"}
	}
	if err := validateEndpoint(cfg.Endpoint); err != nil {
		return err
	}
	if cfg.ShutdownTimeout <= 0 || cfg.ShutdownTimeout >= lifecycle.ServiceShutdownBudget {
		return &ConfigError{Field: FieldShutdownTimeout, Reason: "must be positive and strictly within the service shutdown budget"}
	}
	return nil
}

// ExportErrorHandler is the global OpenTelemetry error handler. It classifies,
// counts, and rate-limits SDK-surfaced failures without ever echoing raw library
// text, so endpoints, response bodies, headers, and credentials cannot leak.
type ExportErrorHandler struct {
	metrics  *metrics.TelemetryMetrics
	warnings *WarningDispatcher
}

// NewExportErrorHandler builds the sanitized global error handler.
func NewExportErrorHandler(
	telemetryMetrics *metrics.TelemetryMetrics,
	warnings *WarningDispatcher,
) *ExportErrorHandler {
	return &ExportErrorHandler{metrics: telemetryMetrics, warnings: warnings}
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
	h.warnings.Warn(debugDiagnostic(reason))
}

// fallbackShutdownTimeout guards programmatic construction that omits the field.
const fallbackShutdownTimeout = 5 * time.Second
