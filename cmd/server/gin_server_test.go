package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/infrastructure/metrics"
	"gin-product-service/internal/infrastructure/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeDatabase avoids a real PostgreSQL dependency; no route used by these tests
// touches the pool.
type fakeDatabase struct {
	mu     sync.Mutex
	closed bool
}

func (f *fakeDatabase) GetDb() *pgxpool.Pool { return nil }

func (f *fakeDatabase) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *fakeDatabase) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func disabledRuntime(t *testing.T) *telemetry.TelemetryRuntime {
	t.Helper()

	runtime, err := telemetry.NewRuntime(context.Background(), telemetry.TelemetryConfig{})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return runtime
}

func enabledRuntime(t *testing.T, endpoint string, shutdownTimeout time.Duration) (*telemetry.TelemetryRuntime, *metrics.TelemetryMetrics) {
	t.Helper()

	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	config := telemetry.TelemetryConfig{
		Enabled:            true,
		ServiceName:        "gin-product-service",
		Environment:        "test",
		Endpoint:           endpoint,
		Compression:        telemetry.CompressionNone,
		ExporterTimeout:    200 * time.Millisecond,
		RootSampleRatio:    1,
		QueueSize:          64,
		BatchSize:          8,
		ScheduleDelay:      20 * time.Millisecond,
		BatchExportTimeout: shutdownTimeout,
		ShutdownTimeout:    shutdownTimeout,
	}

	runtime, err := telemetry.NewRuntime(context.Background(), config,
		telemetry.WithTelemetryMetrics(runtimeMetrics),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return runtime, runtimeMetrics
}

func get(t *testing.T, url string) int {
	t.Helper()

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// Disabled operation: the service serves requests exactly as before and performs
// no telemetry work.
func TestServerDisabledOperation(t *testing.T) {
	db := &fakeDatabase{}
	runtime := disabledRuntime(t)

	srv := NewServerWithOptions(db, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 5 * time.Second,
	})
	srv.Router().GET("/api/v1/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if runtime.Enabled {
		t.Fatalf("runtime.Enabled = true, want false")
	}

	if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}

	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if !db.isClosed() {
		t.Fatalf("database was not closed during shutdown")
	}
}

// Shutdown must stop intake but let accepted requests finish.
func TestServerDrainsAcceptedRequestsOnStop(t *testing.T) {
	db := &fakeDatabase{}
	runtime := disabledRuntime(t)

	srv := NewServerWithOptions(db, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 10 * time.Second,
	})

	started := make(chan struct{})
	release := make(chan struct{})
	srv.Router().GET("/api/v1/slow-probe", func(c *gin.Context) {
		close(started)
		<-release
		c.Status(http.StatusOK)
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	statuses := make(chan int, 1)
	go func() { statuses <- get(t, "http://"+srv.Addr()+"/api/v1/slow-probe") }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("accepted request never started")
	}

	stopResult := make(chan error, 1)
	go func() { stopResult <- srv.Stop(context.Background()) }()

	// The in-flight request must be given time to finish.
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned %v before the accepted request completed", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case status := <-statuses:
		if status != http.StatusOK {
			t.Fatalf("drained request status = %d, want %d", status, http.StatusOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("accepted request never completed")
	}

	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Stop() did not return after the request drained")
	}
}

// An unavailable trace destination must not extend or change shutdown behavior.
func TestServerStopFlushesTelemetryWithinBudget(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := unavailable.URL + "/v1/traces"
	unavailable.Close()

	runtime, runtimeMetrics := enabledRuntime(t, endpoint, time.Second)

	srv := NewServerWithOptions(&fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 8 * time.Second,
	})
	srv.Router().GET("/api/v1/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}

	start := time.Now()
	err := srv.Stop(context.Background())
	elapsed := time.Since(start)

	// Fail-open: the destination outage must not surface as a shutdown error.
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil (fail-open)", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Stop() took %v, want a bounded flush", elapsed)
	}
	if got := testutil.ToFloat64(runtimeMetrics.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTransport))); got < 1 {
		t.Fatalf("exporter failure counter = %v, want the outage reported", got)
	}
}

// A hung destination is bounded by the telemetry shutdown timeout, not the full
// service budget.
func TestServerStopBoundsTelemetryExporterTimeout(t *testing.T) {
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	// Release the hanging handler before closing the server, otherwise Close
	// waits for the outstanding request.
	t.Cleanup(func() {
		close(release)
		hung.Close()
	})

	runtime, _ := enabledRuntime(t, hung.URL+"/v1/traces", 300*time.Millisecond)

	srv := NewServerWithOptions(&fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 12 * time.Second,
	})
	srv.Router().GET("/api/v1/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}

	start := time.Now()
	_ = srv.Stop(context.Background())
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("Stop() took %v, want the telemetry shutdown timeout to bound the flush", elapsed)
	}
}

// Shutdown is idempotent: repeated calls return immediately with the same result.
func TestServerStopIsIdempotent(t *testing.T) {
	db := &fakeDatabase{}
	runtime := disabledRuntime(t)

	srv := NewServerWithOptions(db, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 5 * time.Second,
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	first := srv.Stop(context.Background())

	start := time.Now()
	second := srv.Stop(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("second Stop() took %v, want it to return immediately", elapsed)
	}
	if second != first {
		t.Fatalf("second Stop() error = %v, want the recorded %v", second, first)
	}
}
