package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newTestTelemetryMetrics(t *testing.T) (*TelemetryMetrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewTelemetryMetrics(reg), reg
}

func TestTelemetryMetricsExposesContractNames(t *testing.T) {
	m, reg := newTestTelemetryMetrics(t)
	m.RecordSpanDrop(DropReasonQueueFull)
	m.RecordExporterFailure(ExporterFailureTimeout)
	m.RecordExporterFailure(ExporterFailureTimeout)

	expected := `
# HELP telemetry_spans_dropped_total Spans dropped before export, by bounded reason
# TYPE telemetry_spans_dropped_total counter
telemetry_spans_dropped_total{reason="queue_full"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "telemetry_spans_dropped_total"); err != nil {
		t.Fatalf("spans dropped metric mismatch: %v", err)
	}

	expected = `
# HELP telemetry_exporter_failures_total Trace export failures, by bounded reason
# TYPE telemetry_exporter_failures_total counter
telemetry_exporter_failures_total{reason="timeout"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "telemetry_exporter_failures_total"); err != nil {
		t.Fatalf("exporter failure metric mismatch: %v", err)
	}
}

// Queue pressure and destination failure must stay distinguishable signals.
func TestTelemetryMetricsQueueDropAndExporterFailureAreDistinct(t *testing.T) {
	m, reg := newTestTelemetryMetrics(t)
	m.RecordSpanDrop(DropReasonQueueFull)
	m.RecordExporterFailure(ExporterFailureTransport)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather(): %v", err)
	}

	names := make(map[string]bool, len(families))
	for _, family := range families {
		names[family.GetName()] = true
	}
	for _, want := range []string{"telemetry_spans_dropped_total", "telemetry_exporter_failures_total"} {
		if !names[want] {
			t.Errorf("registry is missing family %q (got %v)", want, names)
		}
	}

	if got := testutil.ToFloat64(m.SpansDropped.WithLabelValues(string(DropReasonQueueFull))); got != 1 {
		t.Errorf("spans dropped = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.ExporterFailures.WithLabelValues(string(ExporterFailureTransport))); got != 1 {
		t.Errorf("exporter failures = %v, want 1", got)
	}
}

// Unbounded or attacker-influenced values must never become label values.
func TestTelemetryMetricsRejectUnboundedReasons(t *testing.T) {
	m, reg := newTestTelemetryMetrics(t)
	m.RecordSpanDrop(DropReason("endpoint=https://user:secret@example.test"))
	m.RecordSpanDrop(DropReason(""))
	m.RecordExporterFailure(ExporterFailureReason("order=12345"))

	expected := `
# HELP telemetry_spans_dropped_total Spans dropped before export, by bounded reason
# TYPE telemetry_spans_dropped_total counter
telemetry_spans_dropped_total{reason="other"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "telemetry_spans_dropped_total"); err != nil {
		t.Fatalf("spans dropped metric mismatch: %v", err)
	}

	expected = `
# HELP telemetry_exporter_failures_total Trace export failures, by bounded reason
# TYPE telemetry_exporter_failures_total counter
telemetry_exporter_failures_total{reason="other"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "telemetry_exporter_failures_total"); err != nil {
		t.Fatalf("exporter failure metric mismatch: %v", err)
	}

	// Exactly one label set must exist for each family.
	if count := testutil.CollectAndCount(m.SpansDropped); count != 1 {
		t.Errorf("spans dropped series count = %d, want 1", count)
	}
}

// Explicit reasons are preserved; only unknown values collapse to "other".
func TestTelemetryMetricsPreserveKnownReasons(t *testing.T) {
	m, reg := newTestTelemetryMetrics(t)

	known := []ExporterFailureReason{
		ExporterFailureTransport,
		ExporterFailureTimeout,
		ExporterFailureServerError,
		ExporterFailureSerialization,
		ExporterFailureUnknown,
	}
	for _, reason := range known {
		m.RecordExporterFailure(reason)
	}

	count := testutil.CollectAndCount(m.ExporterFailures)
	if count != len(known) {
		t.Fatalf("exporter failure series count = %d, want %d", count, len(known))
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(""), "telemetry_spans_dropped_total"); err != nil {
		t.Fatalf("unexpected spans-dropped series: %v", err)
	}
}

// Constructing the collectors twice for one registry must not panic and must
// reuse the existing collectors so counts accumulate.
func TestNewTelemetryMetricsIsIdempotentPerRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()

	first := NewTelemetryMetrics(reg)
	first.RecordSpanDrop(DropReasonQueueFull)

	second := NewTelemetryMetrics(reg) // must not panic
	second.RecordSpanDrop(DropReasonQueueFull)

	if got := testutil.ToFloat64(second.SpansDropped.WithLabelValues(string(DropReasonQueueFull))); got != 2 {
		t.Errorf("spans dropped = %v, want 2 (collectors must be shared)", got)
	}
}

// Queue pressure and destination failure must stay distinguishable on one
// scrape surface, and the exposition must stay label-bounded.
func TestTelemetryMetricsDistinguishDropsFromExporterFailures(t *testing.T) {
	m, reg := newTestTelemetryMetrics(t)

	m.RecordSpanDrop(DropReasonQueueFull)
	m.RecordSpanDrop(DropReasonQueueFull)
	m.RecordExporterFailure(ExporterFailureTransport)
	m.RecordExporterFailure(ExporterFailureServerError)

	gathered, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	counters := make(map[string]map[string]float64)
	for _, family := range gathered {
		series := make(map[string]float64)
		for _, metric := range family.GetMetric() {
			label := ""
			for _, pair := range metric.GetLabel() {
				if label != "" {
					label += ","
				}
				label += pair.GetName() + "=" + pair.GetValue()
			}
			series[label] = metric.GetCounter().GetValue()
		}
		counters[family.GetName()] = series
	}

	drops, ok := counters["telemetry_spans_dropped_total"]
	if !ok {
		t.Fatalf("telemetry_spans_dropped_total family is missing from the scrape")
	}
	if got := drops["reason=queue_full"]; got != 2 {
		t.Errorf("queue_full drops = %v, want 2", got)
	}

	failures, ok := counters["telemetry_exporter_failures_total"]
	if !ok {
		t.Fatalf("telemetry_exporter_failures_total family is missing from the scrape")
	}
	if got := failures["reason=transport"]; got != 1 {
		t.Errorf("transport failures = %v, want 1", got)
	}
	if got := failures["reason=server_error"]; got != 1 {
		t.Errorf("server_error failures = %v, want 1", got)
	}

	// Bounded vocabularies only: no identity, endpoint, or payload values.
	for name, series := range counters {
		for labels := range series {
			for _, forbidden := range []string{"http://", "https://", "Authorization", "token"} {
				if strings.Contains(labels, forbidden) {
					t.Errorf("%s exported a non-bounded label %q", name, labels)
				}
			}
		}
	}
}

// The tracing collectors must register only on the supplied registry, so the
// scrape surface stays exactly what the operator expects.
func TestTelemetryMetricsRegistrationIsScopedToRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewTelemetryMetrics(reg)

	// A labelled counter family only appears after its first observation.
	m.RecordSpanDrop(DropReasonQueueFull)
	m.RecordExporterFailure(ExporterFailureTransport)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if len(families) != 2 {
		t.Fatalf("registered families = %d, want exactly the two tracing collectors", len(families))
	}
	for _, family := range families {
		switch family.GetName() {
		case "telemetry_spans_dropped_total", "telemetry_exporter_failures_total":
		default:
			t.Errorf("unexpected family %q registered", family.GetName())
		}
	}
}

// The D1 drop bridge routes SDK observability into the existing Prometheus
// counters without publishing any SDK-internal metric family on the scrape.
func TestTelemetryMetricsExposeNoSDKInternalSeries(t *testing.T) {
	m, reg := newTestTelemetryMetrics(t)
	m.RecordSpanDrop(DropReasonQueueFull)
	m.RecordExporterFailure(ExporterFailureServerError)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if strings.HasPrefix(family.GetName(), "otel.sdk.") {
			t.Errorf("SDK-internal metric family %q leaked onto the scrape surface", family.GetName())
		}
	}
}
