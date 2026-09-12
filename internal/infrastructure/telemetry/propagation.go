package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
)

// baggageHeader is the W3C baggage header name.
const baggageHeader = "baggage"

// NewPropagator returns the inbound/outbound propagator used by this service:
// always W3C Trace Context, plus a default-deny filtering baggage propagator.
//
// Only keys present in allowlist may be extracted or injected. An empty
// allowlist propagates nothing, and baggage never becomes a span or log field.
func NewPropagator(allowlist []string) propagation.TextMapPropagator {
	allow := make(map[string]struct{}, len(allowlist))
	for _, key := range allowlist {
		if key != "" {
			allow[key] = struct{}{}
		}
	}

	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		&filteringBaggagePropagator{allow: allow},
	)
}

// filteringBaggagePropagator wraps the standard W3C baggage parser but keeps
// only allowlisted members, so no other incoming metadata can cross the service
// or be re-injected downstream.
type filteringBaggagePropagator struct {
	allow map[string]struct{}
}

var _ propagation.TextMapPropagator = (*filteringBaggagePropagator)(nil)

func (p *filteringBaggagePropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	if len(p.allow) == 0 {
		// Default deny: never emit a baggage header.
		return
	}
	filtered := p.filter(baggage.FromContext(ctx))
	if filtered.Len() == 0 {
		return
	}
	// Re-inject only the allowlisted members using the standard encoder.
	ctx = baggage.ContextWithBaggage(ctx, filtered)
	propagation.Baggage{}.Inject(ctx, carrier)
}

func (p *filteringBaggagePropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	ctx = propagation.Baggage{}.Extract(ctx, carrier)
	if len(p.allow) == 0 {
		return baggage.ContextWithoutBaggage(ctx)
	}
	return baggage.ContextWithBaggage(ctx, p.filter(baggage.FromContext(ctx)))
}

func (p *filteringBaggagePropagator) Fields() []string {
	fields := []string{"traceparent", "tracestate"}
	if len(p.allow) > 0 {
		fields = append(fields, baggageHeader)
	}
	return fields
}

// filter keeps only members whose keys were explicitly allowlisted.
func (p *filteringBaggagePropagator) filter(source baggage.Baggage) baggage.Baggage {
	members := source.Members()
	kept := make([]baggage.Member, 0, len(members))
	for _, member := range members {
		if _, allowed := p.allow[member.Key()]; allowed {
			kept = append(kept, member)
		}
	}
	if len(kept) == 0 {
		return baggage.Baggage{}
	}
	filtered, err := baggage.New(kept...)
	if err != nil {
		// A construction failure must never break request handling.
		return baggage.Baggage{}
	}
	return filtered
}
