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
