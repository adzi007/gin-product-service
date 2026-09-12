package telemetry

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// envFunc adapts a map into the getenv seam used by ParseConfig.
func envFunc(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

// defaultConfig mirrors the documented disabled defaults from data-model.md.
func defaultConfig() TelemetryConfig {
	return TelemetryConfig{
		Enabled:            false,
		ServiceName:        DefaultServiceName,
		Compression:        CompressionGzip,
		ExporterTimeout:    5 * time.Second,
		RootSampleRatio:    0.10,
		QueueSize:          2048,
		BatchSize:          512,
		ScheduleDelay:      5 * time.Second,
		BatchExportTimeout: 5 * time.Second,
		ShutdownTimeout:    5 * time.Second,
	}
}

func TestParseConfigDisabledDefaults(t *testing.T) {
	cfg, err := ParseConfig(envFunc(nil))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v, want nil", err)
	}
	if want := defaultConfig(); !reflect.DeepEqual(cfg, want) {
		t.Fatalf("ParseConfig() = %+v, want %+v", cfg, want)
	}
}

// A disabled runtime must never fail startup because of unused tracing values.
func TestParseConfigDisabledIgnoresInvalidValues(t *testing.T) {
	env := map[string]string{
		"OTEL_TRACING_ENABLED":               "false",
		"OTEL_BSP_MAX_QUEUE_SIZE":            "not-a-number",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "not-a-url",
		"OTEL_TRACES_SAMPLER_ARG":            "9.9",
	}
	cfg, err := ParseConfig(envFunc(env))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v, want nil when disabled", err)
	}
	if want := defaultConfig(); !reflect.DeepEqual(cfg, want) {
		t.Fatalf("ParseConfig() = %+v, want %+v", cfg, want)
	}
}

func TestParseConfigEnabledAllFields(t *testing.T) {
	env := map[string]string{
		"OTEL_TRACING_ENABLED":                  "true",
		"OTEL_SERVICE_NAME":                     "svc-a",
		"OTEL_DEPLOYMENT_ENVIRONMENT":           "staging",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":    "https://otel.example.com/v1/traces",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS":     "authorization=Bearer secret-token,x-tenant=acme",
		"OTEL_EXPORTER_OTLP_TRACES_COMPRESSION": "none",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT":     "1500",
		"OTEL_TRACES_SAMPLER_ARG":               "1",
		"OTEL_BSP_MAX_QUEUE_SIZE":               "100",
		"OTEL_BSP_MAX_EXPORT_BATCH_SIZE":        "10",
		"OTEL_BSP_SCHEDULE_DELAY":               "2500",
		"OTEL_BSP_EXPORT_TIMEOUT":               "2000",
		"OTEL_TRACES_SHUTDOWN_TIMEOUT":          "3000",
		"OTEL_BAGGAGE_ALLOWLIST":                "safe-test, tenant-id",
	}
	cfg, err := ParseConfig(envFunc(env))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v, want nil", err)
	}

	want := TelemetryConfig{
		Enabled:            true,
		ServiceName:        "svc-a",
		Environment:        "staging",
		Endpoint:           "https://otel.example.com/v1/traces",
		Headers:            map[string]string{"authorization": "Bearer secret-token", "x-tenant": "acme"},
		Compression:        CompressionNone,
		ExporterTimeout:    1500 * time.Millisecond,
		RootSampleRatio:    1,
		QueueSize:          100,
		BatchSize:          10,
		ScheduleDelay:      2500 * time.Millisecond,
		BatchExportTimeout: 2000 * time.Millisecond,
		ShutdownTimeout:    3000 * time.Millisecond,
		BaggageAllowlist:   []string{"safe-test", "tenant-id"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("ParseConfig() = %+v, want %+v", cfg, want)
	}
}

func TestParseConfigEnvironmentFallsBackToAppEnv(t *testing.T) {
	env := map[string]string{
		"OTEL_TRACING_ENABLED":               "true",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318/v1/traces",
		"APP_ENV":                            "production",
	}
	cfg, err := ParseConfig(envFunc(env))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v, want nil", err)
	}
	if cfg.Environment != "production" {
		t.Fatalf("Environment = %q, want %q", cfg.Environment, "production")
	}
	// Defaults still apply for unspecified optional values.
	if cfg.ServiceName != DefaultServiceName || cfg.RootSampleRatio != 0.10 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

// A valid enabled baseline that individual cases mutate to isolate one rule.
func enabledEnv() map[string]string {
	return map[string]string{
		"OTEL_TRACING_ENABLED":               "true",
		"OTEL_SERVICE_NAME":                  "svc-a",
		"OTEL_DEPLOYMENT_ENVIRONMENT":        "staging",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://otel.example.com/v1/traces",
	}
}

func TestParseConfigEnabledValidation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(env map[string]string)
		wantField string
	}{
		{"oversized service name", func(e map[string]string) { e["OTEL_SERVICE_NAME"] = strings.Repeat("a", 300) }, FieldServiceName},
		{"missing environment", func(e map[string]string) { e["OTEL_DEPLOYMENT_ENVIRONMENT"] = "" }, FieldEnvironment},
		{"missing endpoint", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "" }, FieldEndpoint},
		{"relative endpoint", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "/v1/traces" }, FieldEndpoint},
		{"unsupported scheme", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "ftp://otel.example.com" }, FieldEndpoint},
		{"endpoint without host", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "https:///v1/traces" }, FieldEndpoint},
		{"endpoint user info", func(e map[string]string) {
			e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "https://user:sup3rsecret@otel.example.com/v1/traces"
		}, FieldEndpoint},
		{"endpoint query", func(e map[string]string) {
			e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "https://otel.example.com/v1/traces?k=v"
		}, FieldEndpoint},
		{"endpoint fragment", func(e map[string]string) {
			e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "https://otel.example.com/v1/traces#frag"
		}, FieldEndpoint},
		{"malformed headers", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_HEADERS"] = "no-equals-sign" }, FieldHeaders},
		{"empty header key", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_HEADERS"] = "=value" }, FieldHeaders},
		{"invalid compression", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_COMPRESSION"] = "br" }, FieldCompression},
		{"zero exporter timeout", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"] = "0" }, FieldExporterTimeout},
		{"negative exporter timeout", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"] = "-1" }, FieldExporterTimeout},
		{"exporter timeout beyond budget", func(e map[string]string) { e["OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"] = "60000" }, FieldExporterTimeout},
		{"ratio above one", func(e map[string]string) { e["OTEL_TRACES_SAMPLER_ARG"] = "1.5" }, FieldRootSampleRatio},
		{"ratio below zero", func(e map[string]string) { e["OTEL_TRACES_SAMPLER_ARG"] = "-0.1" }, FieldRootSampleRatio},
		{"ratio not a number", func(e map[string]string) { e["OTEL_TRACES_SAMPLER_ARG"] = "half" }, FieldRootSampleRatio},
		{"zero queue size", func(e map[string]string) { e["OTEL_BSP_MAX_QUEUE_SIZE"] = "0" }, FieldQueueSize},
		{"negative queue size", func(e map[string]string) { e["OTEL_BSP_MAX_QUEUE_SIZE"] = "-5" }, FieldQueueSize},
		{"unbounded queue size", func(e map[string]string) { e["OTEL_BSP_MAX_QUEUE_SIZE"] = "100000000" }, FieldQueueSize},
		{"zero batch size", func(e map[string]string) { e["OTEL_BSP_MAX_EXPORT_BATCH_SIZE"] = "0" }, FieldBatchSize},
		{"batch larger than queue", func(e map[string]string) {
			e["OTEL_BSP_MAX_QUEUE_SIZE"] = "10"
			e["OTEL_BSP_MAX_EXPORT_BATCH_SIZE"] = "11"
		}, FieldBatchSize},
		{"zero schedule delay", func(e map[string]string) { e["OTEL_BSP_SCHEDULE_DELAY"] = "0" }, FieldScheduleDelay},
		{"unbounded schedule delay", func(e map[string]string) { e["OTEL_BSP_SCHEDULE_DELAY"] = "999999999" }, FieldScheduleDelay},
		{"zero batch export timeout", func(e map[string]string) { e["OTEL_BSP_EXPORT_TIMEOUT"] = "0" }, FieldBatchExportTimeout},
		{"batch export timeout beyond budget", func(e map[string]string) { e["OTEL_BSP_EXPORT_TIMEOUT"] = "60000" }, FieldBatchExportTimeout},
		{"zero shutdown timeout", func(e map[string]string) { e["OTEL_TRACES_SHUTDOWN_TIMEOUT"] = "0" }, FieldShutdownTimeout},
		{"shutdown timeout at service budget", func(e map[string]string) { e["OTEL_TRACES_SHUTDOWN_TIMEOUT"] = "15000" }, FieldShutdownTimeout},
		{"empty baggage key", func(e map[string]string) { e["OTEL_BAGGAGE_ALLOWLIST"] = "safe-test,,dup" }, FieldBaggageAllowlist},
		{"duplicate baggage key", func(e map[string]string) { e["OTEL_BAGGAGE_ALLOWLIST"] = "safe-test,safe-test" }, FieldBaggageAllowlist},
		{"invalid baggage key", func(e map[string]string) { e["OTEL_BAGGAGE_ALLOWLIST"] = "safe test" }, FieldBaggageAllowlist},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := enabledEnv()
			tt.mutate(env)

			_, err := ParseConfig(envFunc(env))
			if err == nil {
				t.Fatalf("ParseConfig() error = nil, want field %q error", tt.wantField)
			}
			var cfgErr *ConfigError
			if !errors.As(err, &cfgErr) {
				t.Fatalf("ParseConfig() error = %T, want *ConfigError", err)
			}
			if cfgErr.Field != tt.wantField {
				t.Fatalf("ConfigError.Field = %q, want %q (err: %v)", cfgErr.Field, tt.wantField, err)
			}
		})
	}
}

func TestParseConfigInvalidEnabledBoolean(t *testing.T) {
	env := map[string]string{"OTEL_TRACING_ENABLED": "yes"}
	_, err := ParseConfig(envFunc(env))
	if err == nil {
		t.Fatalf("ParseConfig() error = nil, want strict boolean error")
	}
	if !strings.Contains(err.Error(), FieldEnabled) {
		t.Fatalf("error %q does not mention %q", err, FieldEnabled)
	}
}

// Errors must be actionable by field without echoing secret values.
func TestParseConfigErrorsAreSecretSafe(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(env map[string]string)
		leak   string
	}{
		{
			name: "header credentials",
			mutate: func(e map[string]string) {
				e["OTEL_EXPORTER_OTLP_TRACES_HEADERS"] = "authorization=Bearer sup3r-s3cret-token"
				e["OTEL_BSP_MAX_QUEUE_SIZE"] = "0"
			},
			leak: "sup3r-s3cret-token",
		},
		{
			name: "endpoint credentials",
			mutate: func(e map[string]string) {
				e["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "https://user:sup3r-s3cret-password@otel.example.com"
			},
			leak: "sup3r-s3cret-password",
		},
		{
			name: "malformed header value",
			mutate: func(e map[string]string) {
				e["OTEL_EXPORTER_OTLP_TRACES_HEADERS"] = "malformed-entry-without-separator"
			},
			leak: "malformed-entry-without-separator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := enabledEnv()
			tt.mutate(env)

			_, err := ParseConfig(envFunc(env))
			if err == nil {
				t.Fatalf("ParseConfig() error = nil, want error")
			}
			if strings.Contains(err.Error(), tt.leak) {
				t.Fatalf("error %q leaks sensitive value %q", err, tt.leak)
			}
		})
	}
}
