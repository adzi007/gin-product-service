// Package middleware contains the inbound HTTP middleware chain. Tracing is
// owned here because the route template, final status, and interruption outcome
// are only known at the delivery boundary.
package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	// instrumentationName is the instrumentation scope reported for inbound spans.
	instrumentationName = "gin-product-service/http"

	// coveragePrefix is the only business route prefix traced in detail.
	coveragePrefix = "/api/v1"
	// readinessRoute is traced because it exercises PostgreSQL.
	readinessRoute = "/readyz"

	attrHTTPRequestMethod  = "http.request.method"
	attrHTTPRoute          = "http.route"
	attrHTTPResponseStatus = "http.response.status_code"

	// eventRequestInterrupted is a constant, safe event name for abnormal
	// interruptions. The raw transport error is never attached.
	eventRequestInterrupted = "http.request.interrupted"
	attrInterruptionType    = "interruption.type"

	// Bounded interruption vocabulary.
	interruptionCanceled         = "canceled"
	interruptionDeadlineExceeded = "deadline_exceeded"
)

// TracingConfig carries the explicitly composed tracing dependencies. Every
// field is supplied by the composition root; nothing is resolved globally.
type TracingConfig struct {
	// Enabled reports whether the middleware instruments requests.
	Enabled bool
	// TracerProvider creates the inbound server span. Required when enabled.
	TracerProvider trace.TracerProvider
	// Propagator extracts valid upstream trace context. Required when enabled.
	Propagator propagation.TextMapPropagator
	// EnrichContext optionally derives the request context after the server span
	// starts, used to correlate request-scoped logs with the active span.
	EnrichContext func(ctx context.Context) context.Context
}

// Tracing returns the inbound server-span middleware.
//
// The span starts before authentication and any handler, uses the matched Gin
// route template and method as bounded identifiers, and closes after the final
// response is known. Excluded traffic keeps its current behavior and produces no
// span.
func Tracing(cfg TracingConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enabled || cfg.TracerProvider == nil || !tracedRoute(c.FullPath()) {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		if cfg.Propagator != nil {
			ctx = cfg.Propagator.Extract(ctx, headerCarrier(c.Request.Header))
		}

		route := c.FullPath()
		ctx, span := cfg.TracerProvider.Tracer(instrumentationName).Start(ctx, serverSpanName(c.Request.Method, route),
			trace.WithSpanKind(trace.SpanKindServer),
		)

		if cfg.EnrichContext != nil {
			ctx = cfg.EnrichContext(ctx)
		}
		c.Request = c.Request.WithContext(ctx)

		defer func() {
			recordResponseOutcome(span, c, route)
			span.End()
		}()

		c.Next()
	}
}

// tracedRoute applies the coverage policy: every registered business route under
// /api/v1, plus the readiness check because it exercises PostgreSQL. Health
// checks, metrics scrapes, Swagger assets, and unmatched routes are excluded.
func tracedRoute(route string) bool {
	switch route {
	case "", "/healthz", "/metrics":
		return false
	case readinessRoute:
		return true
	}
	if strings.HasPrefix(route, "/swagger") {
		return false
	}
	return route == coveragePrefix || strings.HasPrefix(route, coveragePrefix+"/")
}

// serverSpanName builds the bounded span name from the route template only.
func serverSpanName(method, route string) string {
	return "HTTP " + method + " " + route
}

// recordResponseOutcome attaches the final, bounded HTTP outcome to the span.
//
// Status rules:
//   - 2xx/3xx and expected 4xx keep their exact status code with the span status
//     left unset, so a business rejection is not reported as a trace error;
//   - 5xx marks the server span as an error;
//   - cancelled, deadline-exceeded, and client-disconnected requests mark the
//     span as an error with a bounded interruption type and a constant event.
func recordResponseOutcome(span trace.Span, c *gin.Context, route string) {
	status := c.Writer.Status()

	span.SetAttributes(
		attribute.String(attrHTTPRequestMethod, c.Request.Method),
		attribute.String(attrHTTPRoute, route),
		attribute.Int(attrHTTPResponseStatus, status),
	)

	if interruption, ok := interruptionType(c.Request.Context()); ok {
		span.SetStatus(codes.Error, interruption)
		span.AddEvent(eventRequestInterrupted, trace.WithAttributes(
			attribute.String(attrInterruptionType, interruption),
		))
		return
	}

	if status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(status))
	}
}

// interruptionType reports a bounded classification for an abnormal request
// termination, or false for a normally completed request.
func interruptionType(ctx context.Context) (string, bool) {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return interruptionDeadlineExceeded, true
	case errors.Is(ctx.Err(), context.Canceled):
		return interruptionCanceled, true
	default:
		return "", false
	}
}

// headerCarrier adapts the inbound request headers to a text-map carrier.
type headerCarrier http.Header

func (h headerCarrier) Get(key string) string { return http.Header(h).Get(key) }

func (h headerCarrier) Set(key, value string) { http.Header(h).Set(key, value) }

func (h headerCarrier) Keys() []string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}

var _ propagation.TextMapCarrier = headerCarrier(nil)
