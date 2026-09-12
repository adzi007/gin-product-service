package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// telemetryEnvVars is every variable the tracing configuration reads. Tests
// neutralise them so the ambient environment cannot change the outcome.
var telemetryEnvVars = []string{
	"OTEL_TRACING_ENABLED",
	"OTEL_SERVICE_NAME",
	"OTEL_DEPLOYMENT_ENVIRONMENT",
	"APP_ENV",
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	"OTEL_EXPORTER_OTLP_TRACES_HEADERS",
	"OTEL_EXPORTER_OTLP_TRACES_COMPRESSION",
	"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT",
	"OTEL_TRACES_SAMPLER_ARG",
	"OTEL_BSP_MAX_QUEUE_SIZE",
	"OTEL_BSP_MAX_EXPORT_BATCH_SIZE",
	"OTEL_BSP_SCHEDULE_DELAY",
	"OTEL_BSP_EXPORT_TIMEOUT",
	"OTEL_TRACES_SHUTDOWN_TIMEOUT",
	"OTEL_BAGGAGE_ALLOWLIST",
}

func clearTelemetryEnv(t *testing.T) {
	t.Helper()
	for _, key := range telemetryEnvVars {
		t.Setenv(key, "")
	}
}

// Disabled is the default: startup succeeds, builds a no-op runtime, and
// attempts no export.
func TestLoadTelemetryDisabledByDefault(t *testing.T) {
	clearTelemetryEnv(t)

	runtime, err := loadTelemetry(context.Background())
	if err != nil {
		t.Fatalf("loadTelemetry() error = %v, want nil for the disabled default", err)
	}
	if runtime.Enabled {
		t.Fatalf("Enabled = true, want false when OTEL_TRACING_ENABLED is unset")
	}
	if runtime.Provider() != nil {
		t.Fatalf("Provider() != nil while disabled: no pgx tracer may be attached")
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
}

// A valid enabled configuration starts without contacting the destination, so a
// destination outage can never block service start.
func TestLoadTelemetryValidEnabledMode(t *testing.T) {
	clearTelemetryEnv(t)
	t.Setenv("OTEL_TRACING_ENABLED", "true")
	t.Setenv("OTEL_DEPLOYMENT_ENVIRONMENT", "test")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:9/v1/traces")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")

	start := time.Now()
	runtime, err := loadTelemetry(context.Background())
	if err != nil {
		t.Fatalf("loadTelemetry() error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("startup took %v, want no destination round-trip", elapsed)
	}
	if !runtime.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if runtime.Provider() == nil {
		t.Fatalf("Provider() = nil, want an SDK provider when enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v, want a fail-open shutdown", err)
	}
}

// APP_ENV remains the fallback deployment environment.
func TestLoadTelemetryFallsBackToAppEnvironment(t *testing.T) {
	clearTelemetryEnv(t)
	t.Setenv("OTEL_TRACING_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:9/v1/traces")
	t.Setenv("APP_ENV", "production")

	runtime, err := loadTelemetry(context.Background())
	if err != nil {
		t.Fatalf("loadTelemetry() error = %v, want the APP_ENV fallback accepted", err)
	}
	if !runtime.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
}

// Enabled but structurally invalid configuration must fail startup with an
// actionable, field-level error.
func TestLoadTelemetryInvalidEnabledConfigurationFailsStartup(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantField string
	}{
		{
			name: "malformed endpoint",
			env: map[string]string{
				"OTEL_DEPLOYMENT_ENVIRONMENT":        "test",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "not-a-url",
			},
			wantField: "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		},
		{
			name: "missing environment",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:9/v1/traces",
			},
			wantField: "OTEL_DEPLOYMENT_ENVIRONMENT",
		},
		{
			name: "unsupported compression",
			env: map[string]string{
				"OTEL_DEPLOYMENT_ENVIRONMENT":           "test",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":    "http://127.0.0.1:9/v1/traces",
				"OTEL_EXPORTER_OTLP_TRACES_COMPRESSION": "brotli",
			},
			wantField: "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION",
		},
		{
			name: "sampling ratio out of range",
			env: map[string]string{
				"OTEL_DEPLOYMENT_ENVIRONMENT":        "test",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:9/v1/traces",
				"OTEL_TRACES_SAMPLER_ARG":            "4.2",
			},
			wantField: "OTEL_TRACES_SAMPLER_ARG",
		},
		{
			name: "batch larger than queue",
			env: map[string]string{
				"OTEL_DEPLOYMENT_ENVIRONMENT":        "test",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:9/v1/traces",
				"OTEL_BSP_MAX_QUEUE_SIZE":            "10",
				"OTEL_BSP_MAX_EXPORT_BATCH_SIZE":     "11",
			},
			wantField: "OTEL_BSP_MAX_EXPORT_BATCH_SIZE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearTelemetryEnv(t)
			t.Setenv("OTEL_TRACING_ENABLED", "true")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			runtime, err := loadTelemetry(context.Background())
			if err == nil {
				t.Fatalf("loadTelemetry() error = nil, want startup to fail")
			}
			if runtime != nil {
				t.Fatalf("loadTelemetry() returned a runtime alongside an error")
			}
			if !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("error %q does not name the offending field %q", err, tt.wantField)
			}
		})
	}
}

// Startup failures must never echo credentials or header values.
func TestLoadTelemetryStartupErrorsAreCredentialSafe(t *testing.T) {
	const secret = "sup3r-s3cret-startup-token"

	clearTelemetryEnv(t)
	t.Setenv("OTEL_TRACING_ENABLED", "true")
	t.Setenv("OTEL_DEPLOYMENT_ENVIRONMENT", "test")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://user:sup3r-s3cret-password@collector.example.test")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=Bearer "+secret)

	runtime, err := loadTelemetry(context.Background())
	if err == nil {
		t.Fatalf("loadTelemetry() error = nil, want a credential-safe failure")
	}
	if runtime != nil {
		t.Fatalf("loadTelemetry() returned a runtime alongside an error")
	}
	for _, leaked := range []string{secret, "sup3r-s3cret-password", "Bearer"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("startup error %q leaks %q", err, leaked)
		}
	}
}
