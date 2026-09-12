package logger

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
)

// observe replaces the package base logger with an observed core for the test.
func observe(t *testing.T) *observer.ObservedLogs {
	t.Helper()

	previous := base
	core, observed := observer.New(zapcore.DebugLevel)
	base = zap.New(core)
	t.Cleanup(func() { base = previous })

	return observed
}

func activeSpanContext(t *testing.T) trace.SpanContext {
	t.Helper()

	traceID, err := trace.TraceIDFromHex(testTraceID)
	if err != nil {
		t.Fatalf("TraceIDFromHex(): %v", err)
	}
	spanID, err := trace.SpanIDFromHex(testSpanID)
	if err != nil {
		t.Fatalf("SpanIDFromHex(): %v", err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
}

func TestWithTraceContextAddsCorrelationIdentifiers(t *testing.T) {
	observed := observe(t)

	ctx := trace.ContextWithSpanContext(context.Background(), activeSpanContext(t))
	ctx = WithTraceContext(ctx)

	L(ctx).Info("handled")

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()

	if got := fields["trace_id"]; got != testTraceID {
		t.Errorf("trace_id = %v, want the lowercase hex %q", got, testTraceID)
	}
	if got := fields["span_id"]; got != testSpanID {
		t.Errorf("span_id = %v, want the lowercase hex %q", got, testSpanID)
	}
}

func TestWithTraceContextWithoutActiveSpanIsUnchanged(t *testing.T) {
	observed := observe(t)

	ctx := context.Background()
	enriched := WithTraceContext(ctx)

	if enriched != ctx {
		t.Fatalf("context was replaced without an active trace")
	}

	L(enriched).Info("handled")

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	for _, key := range []string{"trace_id", "span_id"} {
		if _, ok := fields[key]; ok {
			t.Errorf("field %q was added without an active span", key)
		}
	}
}

func TestWithTraceContextIgnoresInvalidSpanContext(t *testing.T) {
	observed := observe(t)

	ctx := trace.ContextWithSpanContext(context.Background(), trace.SpanContext{})
	enriched := WithTraceContext(ctx)

	if enriched != ctx {
		t.Fatalf("context was replaced for an invalid span context")
	}
	L(enriched).Info("handled")

	fields := observed.All()[0].ContextMap()
	if _, ok := fields["trace_id"]; ok {
		t.Errorf("trace_id added for an invalid span context")
	}
}

func TestWithTraceContextPreservesExistingFieldsAndCauses(t *testing.T) {
	observed := observe(t)

	ctx := WithContext(context.Background(), zap.String("request_id", "req-1"))
	ctx = trace.ContextWithSpanContext(ctx, activeSpanContext(t))
	ctx = WithTraceContext(ctx)

	cause := errors.New("dependency unavailable")
	L(ctx).Error("request failed", zap.Error(cause))

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()

	if got := fields["request_id"]; got != "req-1" {
		t.Errorf("request_id = %v, want the existing field preserved", got)
	}
	if got := fields["trace_id"]; got != testTraceID {
		t.Errorf("trace_id = %v, want %q", got, testTraceID)
	}
	if got := fields["span_id"]; got != testSpanID {
		t.Errorf("span_id = %v, want %q", got, testSpanID)
	}
	if got := fields["error"]; got != "dependency unavailable" {
		t.Errorf("error = %v, want the existing cause preserved", got)
	}
}

// Re-enrichment after a child span starts must refresh span_id without losing
// the trace identifier.
func TestWithTraceContextRefreshesSpanIDForChildSpan(t *testing.T) {
	observed := observe(t)

	parentCtx := trace.ContextWithSpanContext(context.Background(), activeSpanContext(t))
	parentCtx = WithTraceContext(parentCtx)

	childSpanID, err := trace.SpanIDFromHex("1111111111111111")
	if err != nil {
		t.Fatalf("SpanIDFromHex(): %v", err)
	}
	parent := trace.SpanContextFromContext(parentCtx)
	childCtx := trace.ContextWithSpanContext(parentCtx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    parent.TraceID(),
		SpanID:     childSpanID,
		TraceFlags: trace.FlagsSampled,
	}))
	childCtx = WithTraceContext(childCtx)

	L(childCtx).Info("handled")

	fields := observed.All()[0].ContextMap()
	if got := fields["trace_id"]; got != testTraceID {
		t.Errorf("trace_id = %v, want %q preserved", got, testTraceID)
	}
	if got := fields["span_id"]; got != "1111111111111111" {
		t.Errorf("span_id = %v, want the refreshed child span id", got)
	}
}
