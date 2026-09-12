package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	DBQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query latency",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"repository", "operation"},
	)

	// ReservationAttempts counts checkout reservation attempts by outcome.
	ReservationAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "inventory_reservation_attempts_total",
			Help: "Total checkout reservation attempts by outcome",
		},
		[]string{"outcome"},
	)

	// ReservationDuration observes checkout reservation latency by outcome.
	ReservationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "inventory_reservation_duration_seconds",
			Help:    "Checkout reservation latency by outcome",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"outcome"},
	)
)

func ObserveHTTP(method, path, status string, start time.Time) {
	HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
	HTTPRequestDuration.WithLabelValues(method, path).Observe(time.Since(start).Seconds())
}

func ObserveDB(repo, op string) func(start time.Time) {
	// DBQueryDuration.WithLabelValues(repo, op).Observe(time.Since(start).Seconds())
	return func(start time.Time) {
		DBQueryDuration.WithLabelValues(repo, op).Observe(time.Since(start).Seconds())
	}
}

func statusToString(code int) string { return strconv.Itoa(code) }

// DropReason is the bounded label vocabulary for rejected (dropped) spans.
type DropReason string

const (
	// DropReasonQueueFull is recorded when the bounded capacity gate rejects a
	// newly completed span instead of blocking request processing.
	DropReasonQueueFull DropReason = "queue_full"
	// DropReasonOther absorbs any unrecognized reason so label cardinality stays bounded.
	DropReasonOther DropReason = "other"
)

// ExporterFailureReason is the bounded label vocabulary for export failures.
// It stays separate from DropReason so queue pressure and destination failure
// remain distinguishable signals.
type ExporterFailureReason string

const (
	// ExporterFailureTransport is a connection/transport level failure.
	ExporterFailureTransport ExporterFailureReason = "transport"
	// ExporterFailureTimeout is a deadline or cancelled export.
	ExporterFailureTimeout ExporterFailureReason = "timeout"
	// ExporterFailureServerError is a non-success response from the destination.
	ExporterFailureServerError ExporterFailureReason = "server_error"
	// ExporterFailureSerialization is a payload encoding/serialization failure.
	ExporterFailureSerialization ExporterFailureReason = "serialization"
	// ExporterFailureUnknown is a classified failure with no better bounded reason.
	ExporterFailureUnknown ExporterFailureReason = "unknown"
	// ExporterFailureOtherReason absorbs unrecognized values to keep cardinality bounded.
	ExporterFailureOtherReason ExporterFailureReason = "other"
)

// TelemetryMetrics owns the tracing drop/failure counters exposed through the
// existing Prometheus scrape endpoint.
type TelemetryMetrics struct {
	SpansDropped     *prometheus.CounterVec
	ExporterFailures *prometheus.CounterVec
}

// NewTelemetryMetrics builds and registers the tracing collectors on reg. A nil
// registerer falls back to the process default registry, which is what /metrics
// scrapes. Re-constructing for the same registry reuses the existing collectors
// instead of panicking on duplicate registration.
func NewTelemetryMetrics(reg prometheus.Registerer) *TelemetryMetrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	dropped := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "telemetry_spans_dropped_total",
			Help: "Spans dropped before export, by bounded reason",
		},
		[]string{"reason"},
	)

	m := &TelemetryMetrics{
		SpansDropped: dropped,
		ExporterFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "telemetry_exporter_failures_total",
				Help: "Trace export failures, by bounded reason",
			},
			[]string{"reason"},
		),
	}

	if registered, ok := registerOrReuse(reg, dropped).(*prometheus.CounterVec); ok {
		m.SpansDropped = registered
	}
	if registered, ok := registerOrReuse(reg, m.ExporterFailures).(*prometheus.CounterVec); ok {
		m.ExporterFailures = registered
	}
	return m
}

// RecordSpanDrop counts one rejected span, mapping unknown reasons to "other".
func (m *TelemetryMetrics) RecordSpanDrop(reason DropReason) {
	if m == nil || m.SpansDropped == nil {
		return
	}
	m.SpansDropped.WithLabelValues(string(normalizeDropReason(reason))).Inc()
}

// RecordExporterFailure counts one failed export attempt, mapping unknown
// reasons to "other".
func (m *TelemetryMetrics) RecordExporterFailure(reason ExporterFailureReason) {
	if m == nil || m.ExporterFailures == nil {
		return
	}
	m.ExporterFailures.WithLabelValues(string(normalizeExporterFailureReason(reason))).Inc()
}

func normalizeDropReason(reason DropReason) DropReason {
	if reason == DropReasonQueueFull {
		return reason
	}
	return DropReasonOther
}

func normalizeExporterFailureReason(reason ExporterFailureReason) ExporterFailureReason {
	switch reason {
	case ExporterFailureTransport,
		ExporterFailureTimeout,
		ExporterFailureServerError,
		ExporterFailureSerialization,
		ExporterFailureUnknown:
		return reason
	default:
		return ExporterFailureOtherReason
	}
}

// registerOrReuse registers c, returning the already-registered collector when
// the same registry already owns it.
func registerOrReuse(reg prometheus.Registerer, c prometheus.Collector) prometheus.Collector {
	if err := reg.Register(c); err != nil {
		if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return already.ExistingCollector
		}
		return c
	}
	return c
}
