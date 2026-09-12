package telemetry

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	validTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	validTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	validSpanID      = "00f067aa0ba902b7"
)

func TestPropagatorExtractsValidW3CTraceContext(t *testing.T) {
	p := NewPropagator(nil)
	carrier := propagation.MapCarrier{
		"traceparent": validTraceParent,
		"tracestate":  "vendor=value",
	}

	ctx := p.Extract(context.Background(), carrier)
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		t.Fatalf("SpanContext is invalid, want a continued upstream context")
	}
	if got := sc.TraceID().String(); got != validTraceID {
		t.Errorf("TraceID = %q, want %q", got, validTraceID)
	}
	if got := sc.SpanID().String(); got != validSpanID {
		t.Errorf("SpanID = %q, want %q", got, validSpanID)
	}
	if !sc.IsRemote() {
		t.Errorf("SpanContext.IsRemote() = false, want true")
	}
	if !sc.IsSampled() {
		t.Errorf("IsSampled() = false, want true (upstream sampled flag honored)")
	}
	if got := sc.TraceState().String(); got != "vendor=value" {
		t.Errorf("TraceState = %q, want %q", got, "vendor=value")
	}
}

func TestPropagatorHonorsUpstreamUnsampledFlag(t *testing.T) {
	p := NewPropagator(nil)
	carrier := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
	}

	sc := trace.SpanContextFromContext(p.Extract(context.Background(), carrier))
	if !sc.IsValid() {
		t.Fatalf("SpanContext is invalid, want a continued upstream context")
	}
	if sc.IsSampled() {
		t.Errorf("IsSampled() = true, want false (upstream non-sampling must be honored)")
	}
}

func TestPropagatorMalformedTraceContextIsIgnored(t *testing.T) {
	values := []string{
		"",
		"malformed",
		"00-short-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-zz",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
	}

	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			p := NewPropagator(nil)
			carrier := propagation.MapCarrier{"traceparent": value}

			ctx := p.Extract(context.Background(), carrier)
			if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
				t.Fatalf("SpanContext = %v, want invalid for malformed metadata", sc)
			}
		})
	}
}

func TestPropagatorDefaultDenyBaggage(t *testing.T) {
	p := NewPropagator(nil)
	carrier := propagation.MapCarrier{
		"traceparent": validTraceParent,
		"baggage":     "safe-test=allowed,customer-secret=must-not-propagate",
	}

	ctx := p.Extract(context.Background(), carrier)
	if b := baggage.FromContext(ctx); b.Len() != 0 {
		t.Fatalf("baggage = %q, want empty with the default allowlist", b.String())
	}
}

func TestPropagatorExtractsOnlyAllowlistedBaggage(t *testing.T) {
	p := NewPropagator([]string{"safe-test"})
	carrier := propagation.MapCarrier{
		"traceparent": validTraceParent,
		"baggage":     "safe-test=allowed,customer-secret=must-not-propagate",
	}

	ctx := p.Extract(context.Background(), carrier)
	b := baggage.FromContext(ctx)
	if b.Len() != 1 {
		t.Fatalf("baggage = %q, want exactly the allowlisted member", b.String())
	}
	if got := b.Member("safe-test").Value(); got != "allowed" {
		t.Errorf("safe-test = %q, want %q", got, "allowed")
	}
	if got := b.Member("customer-secret").Value(); got != "" {
		t.Errorf("customer-secret = %q, want it dropped", got)
	}
}

func TestPropagatorIgnoresMalformedBaggage(t *testing.T) {
	p := NewPropagator([]string{"safe-test"})
	carrier := propagation.MapCarrier{
		"baggage": "=dropped,***=dropped,safe-test=kept",
	}

	ctx := p.Extract(context.Background(), carrier)
	b := baggage.FromContext(ctx)
	if b.Len() != 1 || b.Member("safe-test").Value() != "kept" {
		t.Fatalf("baggage = %q, want only safe-test=kept", b.String())
	}
}

func TestPropagatorInjectsOnlyAllowlistedBaggage(t *testing.T) {
	p := NewPropagator([]string{"safe-test"})
	ctx := contextWithSpanAndBaggage(t, []baggageMember{
		{key: "safe-test", value: "allowed"},
		{key: "customer-secret", value: "must-not-propagate"},
	})

	carrier := propagation.MapCarrier{}
	p.Inject(ctx, carrier)

	if got := carrier.Get("traceparent"); got != validTraceParent {
		t.Errorf("traceparent = %q, want %q", got, validTraceParent)
	}
	outgoing := carrier.Get("baggage")
	if !strings.Contains(outgoing, "safe-test=allowed") {
		t.Errorf("baggage = %q, want it to contain the allowlisted member", outgoing)
	}
	if strings.Contains(outgoing, "customer-secret") {
		t.Errorf("baggage = %q must not contain disallowed keys", outgoing)
	}
}

func TestPropagatorInjectsNoBaggageByDefault(t *testing.T) {
	p := NewPropagator(nil)
	ctx := contextWithSpanAndBaggage(t, []baggageMember{
		{key: "safe-test", value: "allowed"},
	})

	carrier := propagation.MapCarrier{}
	p.Inject(ctx, carrier)

	if got := carrier.Get("baggage"); got != "" {
		t.Errorf("baggage = %q, want no baggage with the default deny-all policy", got)
	}
	if got := carrier.Get("traceparent"); got != validTraceParent {
		t.Errorf("traceparent = %q, want trace context still injected", got)
	}
}

func TestPropagatorFieldsCoverTraceContext(t *testing.T) {
	p := NewPropagator(nil)
	fields := strings.Join(p.Fields(), ",")
	for _, want := range []string{"traceparent", "tracestate"} {
		if !strings.Contains(fields, want) {
			t.Errorf("Fields() = %q, want it to include %q", fields, want)
		}
	}
}

type baggageMember struct {
	key   string
	value string
}

// contextWithSpanAndBaggage builds a context with a valid sampled remote span
// context plus the supplied baggage members.
func contextWithSpanAndBaggage(t *testing.T, members []baggageMember) context.Context {
	t.Helper()

	built := make([]baggage.Member, 0, len(members))
	for _, m := range members {
		member, err := baggage.NewMember(m.key, m.value)
		if err != nil {
			t.Fatalf("baggage.NewMember(%q): %v", m.key, err)
		}
		built = append(built, member)
	}
	b, err := baggage.New(built...)
	if err != nil {
		t.Fatalf("baggage.New(): %v", err)
	}

	traceID, err := trace.TraceIDFromHex(validTraceID)
	if err != nil {
		t.Fatalf("TraceIDFromHex(): %v", err)
	}
	spanID, err := trace.SpanIDFromHex(validSpanID)
	if err != nil {
		t.Fatalf("SpanIDFromHex(): %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})

	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	return baggage.ContextWithBaggage(ctx, b)
}
