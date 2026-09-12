package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// eventPanicRecovered is the constant, safe event recorded when a panic is
// recovered. The panic value and stack are deliberately never attached.
const eventPanicRecovered = "panic.recovered"

// panicStatusDescription is the constant, safe span status description.
const panicStatusDescription = "panic"

// RecoveryConfig configures the tracing-aware panic recovery.
type RecoveryConfig struct {
	// Report receives a sanitized notification that a panic was recovered, used
	// to emit a constant operator log line. The panic value is intentionally not
	// passed on, so no customer data can leak through the report.
	Report func(c *gin.Context)
}

// Recovery returns middleware that recovers a panic raised inside the traced
// request chain.
//
// It records a constant safe event on the active server span, marks that span as
// an error, reports the failure through the sanitized hook, and preserves the
// established 500 response contract (empty body, status 500).
//
// It must be registered after Tracing so an active span exists when a handler
// panics; the outer engine recovery remains in place as defense in depth.
func Recovery(cfg RecoveryConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if span := trace.SpanFromContext(c.Request.Context()); span.IsRecording() {
					span.AddEvent(eventPanicRecovered)
					span.SetStatus(codes.Error, panicStatusDescription)
				}
				if cfg.Report != nil {
					cfg.Report(c)
				}
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()

		c.Next()
	}
}
