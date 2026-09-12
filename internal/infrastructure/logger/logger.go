package logger

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ctxKey struct{}

var base *zap.Logger

func Init(env string) error {
	var cfg zap.Config
	if env == "production" {
		cfg = zap.NewProductionConfig()
		cfg.EncoderConfig.TimeKey = "timestamp"
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}
	cfg.OutputPaths = []string{"stdout"}

	l, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return err
	}
	base = l
	return nil
}

// L returns the request-scoped logger if present, else the base logger.
func L(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok {
		return l
	}
	if base == nil {
		// fallback so nothing panics if Init wasn't called (e.g. in tests)
		base, _ = zap.NewDevelopment()
	}
	return base
}

// WithContext attaches fields (request id, trace id, etc.) to a child logger
// and returns a new context carrying it.
func WithContext(ctx context.Context, fields ...zap.Field) context.Context {
	return context.WithValue(ctx, ctxKey{}, L(ctx).With(fields...))
}

// WithTraceContext enriches the request-scoped logger with the active span's
// correlation identifiers.
//
// When a valid span is active, lowercase hex trace_id and span_id fields are
// added while every existing field and error cause is preserved. Without an
// active span the context is returned unchanged, so logs outside a request are
// untouched. Trace identifiers are correlation fields, not credentials.
func WithTraceContext(ctx context.Context) context.Context {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ctx
	}
	return WithContext(ctx,
		zap.String("trace_id", spanContext.TraceID().String()),
		zap.String("span_id", spanContext.SpanID().String()),
	)
}

func Sync() {
	if base != nil {
		_ = base.Sync()
	}
}

func die(msg string, err error) {
	base.Fatal(msg, zap.Error(err))
	os.Exit(1)
}
