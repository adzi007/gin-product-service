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

	"gin-product-service/internal/lifecycle"
)

// Environment variable names forming the five owned startup gates. All other
// OpenTelemetry settings are delegated to the SDK's native environment parsing.
const (
	envTracingEnabled   = "OTEL_TRACING_ENABLED"
	envEnvironment      = "OTEL_DEPLOYMENT_ENVIRONMENT"
	envAppEnvironment   = "APP_ENV"
	envEndpoint         = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envShutdownTimeout  = "OTEL_TRACES_SHUTDOWN_TIMEOUT"
	envBaggageAllowlist = "OTEL_BAGGAGE_ALLOWLIST"
)

// DefaultServiceName is exported as service.name when the SDK has no resource
// service-name setting of its own.
const DefaultServiceName = "gin-product-service"

// defaultShutdownTimeout is the documented telemetry delivery reservation.
const defaultShutdownTimeout = 5000 * time.Millisecond

// maxDurationMillis bounds millisecond-to-duration conversion so an operator
// value can never overflow before the shutdown-budget comparison runs.
const maxDurationMillis = int64(^uint64(0)>>1) / int64(time.Millisecond)

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
	FieldEnabled          = "OTEL_TRACING_ENABLED"
	FieldEnvironment      = "OTEL_DEPLOYMENT_ENVIRONMENT"
	FieldEndpoint         = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	FieldShutdownTimeout  = "OTEL_TRACES_SHUTDOWN_TIMEOUT"
	FieldBaggageAllowlist = "OTEL_BAGGAGE_ALLOWLIST"
)

// TelemetryConfig is the immutable, parsed operational configuration for the
// five owned startup gates. Every SDK-native tuning setting is delegated to the
// SDK's environment parsing and is intentionally absent here.
type TelemetryConfig struct {
	Enabled          bool
	Environment      string
	Endpoint         string
	ShutdownTimeout  time.Duration
	BaggageAllowlist []string
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
		Enabled:         false,
		ShutdownTimeout: defaultShutdownTimeout,
	}

	enabled, err := parseStrictBool(getenv(envTracingEnabled))
	if err != nil {
		return TelemetryConfig{}, &ConfigError{Field: FieldEnabled, Reason: "must be exactly true or false"}
	}
	cfg.Enabled = enabled
	if !enabled {
		return cfg, nil
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

	if raw := strings.TrimSpace(getenv(envShutdownTimeout)); raw != "" {
		timeout, err := parseShutdownTimeoutMillis(raw)
		if err != nil {
			return TelemetryConfig{}, &ConfigError{Field: FieldShutdownTimeout, Reason: err.Error()}
		}
		cfg.ShutdownTimeout = timeout
	}
	if cfg.ShutdownTimeout <= 0 || cfg.ShutdownTimeout >= lifecycle.ServiceShutdownBudget {
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

// parseShutdownTimeoutMillis parses a whole number of milliseconds, rejecting
// non-numeric, non-positive, and overflow-prone values before any conversion.
func parseShutdownTimeoutMillis(raw string) (time.Duration, error) {
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a whole number of milliseconds")
	}
	if ms <= 0 || ms > maxDurationMillis {
		return 0, fmt.Errorf("is outside the supported range")
	}
	return time.Duration(ms) * time.Millisecond, nil
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
