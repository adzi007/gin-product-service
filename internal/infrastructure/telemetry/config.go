// Package telemetry owns the OpenTelemetry configuration, runtime, propagation,
// buffering, and wiring adapters used by this service. It is infrastructure-only:
// no domain entity or business implementation depends on it.
package telemetry

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variable names forming the operator-facing configuration contract.
const (
	envTracingEnabled   = "OTEL_TRACING_ENABLED"
	envServiceName      = "OTEL_SERVICE_NAME"
	envEnvironment      = "OTEL_DEPLOYMENT_ENVIRONMENT"
	envAppEnvironment   = "APP_ENV"
	envEndpoint         = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envHeaders          = "OTEL_EXPORTER_OTLP_TRACES_HEADERS"
	envCompression      = "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION"
	envExporterTimeout  = "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"
	envSampleRatio      = "OTEL_TRACES_SAMPLER_ARG"
	envQueueSize        = "OTEL_BSP_MAX_QUEUE_SIZE"
	envBatchSize        = "OTEL_BSP_MAX_EXPORT_BATCH_SIZE"
	envScheduleDelay    = "OTEL_BSP_SCHEDULE_DELAY"
	envBatchTimeout     = "OTEL_BSP_EXPORT_TIMEOUT"
	envShutdownTimeout  = "OTEL_TRACES_SHUTDOWN_TIMEOUT"
	envBaggageAllowlist = "OTEL_BAGGAGE_ALLOWLIST"
)

// Documented defaults.
const (
	// DefaultServiceName is exported as service.name when unset or empty.
	DefaultServiceName = "gin-product-service"

	defaultExporterTimeout    = 5000 * time.Millisecond
	defaultRootSampleRatio    = 0.10
	defaultQueueSize          = 2048
	defaultBatchSize          = 512
	defaultScheduleDelay      = 5000 * time.Millisecond
	defaultBatchExportTimeout = 5000 * time.Millisecond
	defaultShutdownTimeout    = 5000 * time.Millisecond
)

// Bounds keep configuration from defeating the resource and shutdown guarantees.
const (
	maxServiceNameLength = 255
	minQueueSize         = 1
	maxQueueSize         = 1_000_000
	maxScheduleDelay     = time.Hour

	// ServiceShutdownBudget is the service's single graceful shutdown window.
	// Telemetry timeouts must fit strictly inside it.
	ServiceShutdownBudget = 15 * time.Second
)

// ConfigError reports a single unusable field without echoing its raw value, so
// credentials and header values never reach startup errors or logs.
type ConfigError struct {
	Field  string
	Reason string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("invalid telemetry configuration: %s %s", e.Field, e.Reason)
}

// Field identifiers used in ConfigError for actionable, value-free messages.
const (
	FieldEnabled            = "OTEL_TRACING_ENABLED"
	FieldServiceName        = "OTEL_SERVICE_NAME"
	FieldEnvironment        = "OTEL_DEPLOYMENT_ENVIRONMENT"
	FieldEndpoint           = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	FieldHeaders            = "OTEL_EXPORTER_OTLP_TRACES_HEADERS"
	FieldCompression        = "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION"
	FieldExporterTimeout    = "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"
	FieldRootSampleRatio    = "OTEL_TRACES_SAMPLER_ARG"
	FieldQueueSize          = "OTEL_BSP_MAX_QUEUE_SIZE"
	FieldBatchSize          = "OTEL_BSP_MAX_EXPORT_BATCH_SIZE"
	FieldScheduleDelay      = "OTEL_BSP_SCHEDULE_DELAY"
	FieldBatchExportTimeout = "OTEL_BSP_EXPORT_TIMEOUT"
	FieldShutdownTimeout    = "OTEL_TRACES_SHUTDOWN_TIMEOUT"
	FieldBaggageAllowlist   = "OTEL_BAGGAGE_ALLOWLIST"
)

// Compression selects the OTLP/HTTP payload compression.
type Compression string

const (
	CompressionGzip Compression = "gzip"
	CompressionNone Compression = "none"
)

// TelemetryConfig is the immutable, parsed operational configuration. It is
// constructed once at startup and shared read-only.
type TelemetryConfig struct {
	Enabled            bool
	ServiceName        string
	Environment        string
	Endpoint           string
	Headers            map[string]string
	Compression        Compression
	ExporterTimeout    time.Duration
	RootSampleRatio    float64
	QueueSize          int
	BatchSize          int
	ScheduleDelay      time.Duration
	BatchExportTimeout time.Duration
	ShutdownTimeout    time.Duration
	BaggageAllowlist   []string
}

// LoadConfig parses the configuration from the process environment.
func LoadConfig() (TelemetryConfig, error) {
	return ParseConfig(os.Getenv)
}

// ParseConfig parses and validates the configuration using the supplied lookup.
//
// A disabled configuration returns the documented defaults without validating
// the unused values, because nothing is constructed and tracing must never block
// startup when it is switched off. An explicitly enabled configuration fails on
// the first unusable field with a secret-safe *ConfigError.
func ParseConfig(getenv func(string) string) (TelemetryConfig, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	cfg := TelemetryConfig{
		Enabled:            false,
		ServiceName:        DefaultServiceName,
		Compression:        CompressionGzip,
		ExporterTimeout:    defaultExporterTimeout,
		RootSampleRatio:    defaultRootSampleRatio,
		QueueSize:          defaultQueueSize,
		BatchSize:          defaultBatchSize,
		ScheduleDelay:      defaultScheduleDelay,
		BatchExportTimeout: defaultBatchExportTimeout,
		ShutdownTimeout:    defaultShutdownTimeout,
	}

	enabled, err := parseStrictBool(getenv(envTracingEnabled))
	if err != nil {
		return TelemetryConfig{}, &ConfigError{Field: FieldEnabled, Reason: "must be exactly true or false"}
	}
	cfg.Enabled = enabled
	if !enabled {
		return cfg, nil
	}

	// service.name: optional, bounded, falls back to the documented default.
	if name := strings.TrimSpace(getenv(envServiceName)); name != "" {
		if len(name) > maxServiceNameLength {
			return TelemetryConfig{}, &ConfigError{Field: FieldServiceName, Reason: "exceeds the maximum length"}
		}
		cfg.ServiceName = name
	}

	// deployment.environment.name: OTEL_DEPLOYMENT_ENVIRONMENT, then APP_ENV.
	environment := strings.TrimSpace(getenv(envEnvironment))
	if environment == "" {
		environment = strings.TrimSpace(getenv(envAppEnvironment))
	}
	if environment == "" {
		return TelemetryConfig{}, &ConfigError{Field: FieldEnvironment, Reason: "is required when tracing is enabled"}
	}
	cfg.Environment = environment

	endpoint := strings.TrimSpace(getenv(envEndpoint))
	if err := validateEndpoint(endpoint); err != nil {
		return TelemetryConfig{}, err
	}
	cfg.Endpoint = endpoint

	if raw := strings.TrimSpace(getenv(envHeaders)); raw != "" {
		headers, err := parseOTLPHeaders(raw)
		if err != nil {
			return TelemetryConfig{}, &ConfigError{Field: FieldHeaders, Reason: err.Error()}
		}
		cfg.Headers = headers
	}

	if raw := strings.TrimSpace(getenv(envCompression)); raw != "" {
		switch strings.ToLower(raw) {
		case string(CompressionGzip):
			cfg.Compression = CompressionGzip
		case string(CompressionNone):
			cfg.Compression = CompressionNone
		default:
			return TelemetryConfig{}, &ConfigError{Field: FieldCompression, Reason: "must be gzip or none"}
		}
	}

	if timeout, present, err := parseMilliseconds(getenv(envExporterTimeout)); err != nil {
		return TelemetryConfig{}, &ConfigError{Field: FieldExporterTimeout, Reason: "must be a whole number of milliseconds"}
	} else if present {
		cfg.ExporterTimeout = timeout
	}
	if cfg.ExporterTimeout <= 0 || cfg.ExporterTimeout > ServiceShutdownBudget {
		return TelemetryConfig{}, &ConfigError{Field: FieldExporterTimeout, Reason: "must be positive and within the service shutdown budget"}
	}

	if raw := strings.TrimSpace(getenv(envSampleRatio)); raw != "" {
		ratio, err := strconv.ParseFloat(raw, 64)
		if err != nil || ratio != ratio /* NaN */ {
			return TelemetryConfig{}, &ConfigError{Field: FieldRootSampleRatio, Reason: "must be a decimal between 0 and 1"}
		}
		cfg.RootSampleRatio = ratio
	}
	if cfg.RootSampleRatio < 0 || cfg.RootSampleRatio > 1 {
		return TelemetryConfig{}, &ConfigError{Field: FieldRootSampleRatio, Reason: "must be a decimal between 0 and 1"}
	}

	if raw := strings.TrimSpace(getenv(envQueueSize)); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil {
			return TelemetryConfig{}, &ConfigError{Field: FieldQueueSize, Reason: "must be a whole number"}
		}
		cfg.QueueSize = size
	}
	if cfg.QueueSize < minQueueSize || cfg.QueueSize > maxQueueSize {
		return TelemetryConfig{}, &ConfigError{Field: FieldQueueSize, Reason: "is outside the supported range"}
	}

	if raw := strings.TrimSpace(getenv(envBatchSize)); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil {
			return TelemetryConfig{}, &ConfigError{Field: FieldBatchSize, Reason: "must be a whole number"}
		}
		cfg.BatchSize = size
	}
	if cfg.BatchSize < 1 || cfg.BatchSize > cfg.QueueSize {
		return TelemetryConfig{}, &ConfigError{Field: FieldBatchSize, Reason: "must be positive and no larger than the queue size"}
	}

	if delay, present, err := parseMilliseconds(getenv(envScheduleDelay)); err != nil {
		return TelemetryConfig{}, &ConfigError{Field: FieldScheduleDelay, Reason: "must be a whole number of milliseconds"}
	} else if present {
		cfg.ScheduleDelay = delay
	}
	if cfg.ScheduleDelay <= 0 || cfg.ScheduleDelay > maxScheduleDelay {
		return TelemetryConfig{}, &ConfigError{Field: FieldScheduleDelay, Reason: "is outside the supported range"}
	}

	if timeout, present, err := parseMilliseconds(getenv(envBatchTimeout)); err != nil {
		return TelemetryConfig{}, &ConfigError{Field: FieldBatchExportTimeout, Reason: "must be a whole number of milliseconds"}
	} else if present {
		cfg.BatchExportTimeout = timeout
	}
	if cfg.BatchExportTimeout <= 0 || cfg.BatchExportTimeout > ServiceShutdownBudget {
		return TelemetryConfig{}, &ConfigError{Field: FieldBatchExportTimeout, Reason: "must be positive and within the service shutdown budget"}
	}

	if timeout, present, err := parseMilliseconds(getenv(envShutdownTimeout)); err != nil {
		return TelemetryConfig{}, &ConfigError{Field: FieldShutdownTimeout, Reason: "must be a whole number of milliseconds"}
	} else if present {
		cfg.ShutdownTimeout = timeout
	}
	if cfg.ShutdownTimeout <= 0 || cfg.ShutdownTimeout >= ServiceShutdownBudget {
		return TelemetryConfig{}, &ConfigError{Field: FieldShutdownTimeout, Reason: "must be positive and strictly within the service shutdown budget"}
	}

	if raw := strings.TrimSpace(getenv(envBaggageAllowlist)); raw != "" {
		allowlist, err := parseBaggageAllowlist(raw)
		if err != nil {
			return TelemetryConfig{}, &ConfigError{Field: FieldBaggageAllowlist, Reason: err.Error()}
		}
		cfg.BaggageAllowlist = allowlist
	}

	return cfg, nil
}

// BaggagePropagationEnabled reports whether any baggage key may cross this service.
func (c TelemetryConfig) BaggagePropagationEnabled() bool {
	return len(c.BaggageAllowlist) > 0
}

func parseStrictBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("not a strict boolean")
	}
}

// parseMilliseconds reports (duration, present, error) so an explicit zero is
// distinguishable from an unset value that should fall back to its default.
func parseMilliseconds(raw string) (time.Duration, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	ms, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, err
	}
	return time.Duration(ms) * time.Millisecond, true, nil
}

// validateEndpoint enforces an absolute http/https URL with a host and no user
// info, query, or fragment. Error text never repeats the supplied value.
func validateEndpoint(raw string) error {
	if raw == "" {
		return &ConfigError{Field: FieldEndpoint, Reason: "is required when tracing is enabled"}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return &ConfigError{Field: FieldEndpoint, Reason: "must be an absolute URL"}
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return &ConfigError{Field: FieldEndpoint, Reason: "must use the http or https scheme"}
	}
	if parsed.Host == "" {
		return &ConfigError{Field: FieldEndpoint, Reason: "must include a host"}
	}
	if parsed.User != nil {
		return &ConfigError{Field: FieldEndpoint, Reason: "must not contain user info"}
	}
	if parsed.RawQuery != "" {
		return &ConfigError{Field: FieldEndpoint, Reason: "must not contain a query"}
	}
	if parsed.Fragment != "" {
		return &ConfigError{Field: FieldEndpoint, Reason: "must not contain a fragment"}
	}
	return nil
}

// parseOTLPHeaders parses the OTLP "key=value,key=value" header list. Failures
// report only the position/shape, never the sensitive value.
func parseOTLPHeaders(raw string) (map[string]string, error) {
	headers := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, value, found := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, fmt.Errorf("contains a malformed header entry")
		}
		headers[key] = strings.TrimSpace(value)
	}
	if len(headers) == 0 {
		return nil, fmt.Errorf("contains no usable header entry")
	}
	return headers, nil
}

// parseBaggageAllowlist rejects empty, duplicate, and syntactically invalid keys
// so a malformed allowlist fails fast instead of silently propagating metadata.
func parseBaggageAllowlist(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	allowlist := make([]string, 0, 4)
	for _, entry := range strings.Split(raw, ",") {
		key := strings.TrimSpace(entry)
		if key == "" {
			return nil, fmt.Errorf("contains an empty baggage key")
		}
		if !isValidBaggageKey(key) {
			return nil, fmt.Errorf("contains an invalid baggage key")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("contains a duplicate baggage key")
		}
		seen[key] = struct{}{}
		allowlist = append(allowlist, key)
	}
	if len(allowlist) == 0 {
		return nil, fmt.Errorf("contains no usable baggage key")
	}
	return allowlist, nil
}

// isValidBaggageKey applies the W3C baggage token grammar.
func isValidBaggageKey(key string) bool {
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return key != ""
}
