package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const operationFailedDescription = "operation failed"

// StartOperation starts a child span using the provider already attached to the
// incoming request context. When tracing is disabled (or the call is outside a
// traced request), OpenTelemetry supplies a no-op provider.
func StartOperation(ctx context.Context, instrumentationName, spanName string) (context.Context, trace.Span) {
	provider := trace.SpanFromContext(ctx).TracerProvider()
	return provider.Tracer(instrumentationName).Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindInternal))
}

// RecordError records the returned operation error and marks the span failed.
// Callers remain responsible for ending the span.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, operationFailedDescription)
}

// TraceID returns the active trace identifier, or an empty string when the
// context does not contain a valid span.
func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}
