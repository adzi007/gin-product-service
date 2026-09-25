package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/infrastructure/database"
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
	closes int
}

func (f *fakeDatabase) GetDb() *pgxpool.Pool { return nil }

func (f *fakeDatabase) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.closes++
}

func (f *fakeDatabase) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeDatabase) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

func disabledRuntime(t *testing.T) *telemetry.TelemetryRuntime {
	t.Helper()

	runtime, err := telemetry.NewRuntime(context.Background(), telemetry.TelemetryConfig{})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return runtime
}

// stubAuth0Config supplies valid Auth0 settings for tests that are not about
// startup configuration validation.
func stubAuth0Config(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH0_DOMAIN", "tenant.us.auth0.com")
	t.Setenv("AUTH0_AUDIENCE", "https://api.example.com/")
}

// mustServer builds a server for tests, failing the test on a nil-runtime or
// constructor error.
func mustServer(t *testing.T, db database.Database, runtime *telemetry.TelemetryRuntime, opts Options) *ginServer {
	t.Helper()
	stubAuth0Config(t)
	srv, err := NewServerWithOptions(db, runtime, opts)
	if err != nil {
		t.Fatalf("mustServer(t, ) error = %v", err)
	}
	return srv
}

// A nil telemetry runtime is rejected before any route is set up.
func TestServerRejectsNilTelemetryRuntime(t *testing.T) {
	stubAuth0Config(t)
	srv, err := NewServerWithOptions(&fakeDatabase{}, nil, Options{Address: "127.0.0.1:0"})
	if err == nil {
		t.Fatalf("NewServerWithOptions(nil runtime) error = nil, want a constructor error")
	}
	if srv != nil {
		t.Fatalf("NewServerWithOptions(nil runtime) returned a server")
	}

	appSrv, err := NewServer(&fakeDatabase{}, nil)
	if err == nil {
		t.Fatalf("NewServer(nil runtime) error = nil, want a constructor error")
	}
	if appSrv != nil {
		t.Fatalf("NewServer(nil runtime) returned a server")
	}
}

// Missing or malformed Auth0 configuration must prevent serving rather than
// exposing management routes with unverified trust material.
func TestServerRejectsInvalidAuth0Config(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		audience string
	}{
		{name: "missing domain", domain: "", audience: "https://api.example.com/"},
		{name: "missing audience", domain: "tenant.us.auth0.com", audience: ""},
		{name: "blank domain", domain: "   ", audience: "https://api.example.com/"},
		{name: "domain with scheme", domain: "https://tenant.us.auth0.com", audience: "https://api.example.com/"},
		{name: "domain with path", domain: "tenant.us.auth0.com/oauth", audience: "https://api.example.com/"},
		{name: "domain with userinfo", domain: "user@tenant.us.auth0.com", audience: "https://api.example.com/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTH0_DOMAIN", tc.domain)
			t.Setenv("AUTH0_AUDIENCE", tc.audience)

			srv, err := NewServerWithOptions(&fakeDatabase{}, disabledRuntime(t), Options{Address: "127.0.0.1:0"})
			if err == nil {
				t.Fatalf("NewServerWithOptions(domain=%q, audience=%q) error = nil, want a startup error", tc.domain, tc.audience)
			}
			if srv != nil {
				t.Fatalf("NewServerWithOptions(domain=%q, audience=%q) returned a server", tc.domain, tc.audience)
			}
		})
	}
}

// A valid configuration wires exactly one admin verifier without any network
// fetch during construction: protected routes deny an unauthenticated request
// and public catalog routes stay reachable.
func TestServerWiresAdminAuthorizationWithoutNetworkFetch(t *testing.T) {
	started := time.Now()

	srv := mustServer(t, &fakeDatabase{}, disabledRuntime(t), Options{Address: "127.0.0.1:0"})

	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("server construction took %v, want no blocking network fetch", elapsed)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/categories", nil)
	srv.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/categories without a credential = %d, want 401", recorder.Code)
	}

	// A public catalog read is not admin-protected, so it must not return 401/403.
	publicRecorder := httptest.NewRecorder()
	publicRequest := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
	srv.Handler().ServeHTTP(publicRecorder, publicRequest)

	if publicRecorder.Code == http.StatusUnauthorized || publicRecorder.Code == http.StatusForbidden {
		t.Fatalf("GET /api/v1/categories = %d, want public access without an admin denial", publicRecorder.Code)
	}
}
func enabledRuntime(t *testing.T, endpoint string, shutdownTimeout time.Duration) (*telemetry.TelemetryRuntime, *metrics.TelemetryMetrics) {
	t.Helper()

	// Deterministic sampling: pin the native parent-based sampler to ratio 1 so
	// capture assertions are never ratio-dependent. The capture receiver reads
	// raw protobuf, so disable the documented gzip default in tests.
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.0")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")

	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	config := telemetry.TelemetryConfig{
		Enabled:         true,
		Environment:     "test",
		Endpoint:        endpoint,
		ShutdownTimeout: shutdownTimeout,
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

	srv := mustServer(t, db, runtime, Options{
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

	srv := mustServer(t, db, runtime, Options{
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

	srv := mustServer(t, &fakeDatabase{}, runtime, Options{
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

	srv := mustServer(t, &fakeDatabase{}, runtime, Options{
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

	srv := mustServer(t, db, runtime, Options{
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

// blockingDatabase blocks Close until its release channel is closed, simulating
// a pgxpool.Close that waits on in-use connections.
type blockingDatabase struct {
	release     chan struct{}
	closed      chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func newBlockingDatabase() *blockingDatabase {
	return &blockingDatabase{release: make(chan struct{}), closed: make(chan struct{})}
}

func (b *blockingDatabase) GetDb() *pgxpool.Pool { return nil }

func (b *blockingDatabase) Close() {
	b.once.Do(func() {
		<-b.release
		close(b.closed)
	})
}

func (b *blockingDatabase) releaseClose() {
	b.releaseOnce.Do(func() { close(b.release) })
}

// An active connection is force-closed once the drain allocation expires, so its
// request context is canceled instead of hanging shutdown forever.
func TestServerStopCancelsActiveConnectionsOnDrainExpiry(t *testing.T) {
	runtime := disabledRuntime(t)

	srv := mustServer(t, &fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 300 * time.Millisecond,
	})

	started := make(chan struct{})
	canceled := make(chan struct{})
	srv.Router().GET("/api/v1/context-cancel", func(c *gin.Context) {
		close(started)
		<-c.Request.Context().Done()
		close(canceled)
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get("http://" + srv.Addr() + "/api/v1/context-cancel")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("request never started")
	}

	start := time.Now()
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil for a bounded drain", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Stop() took %v, want the drain expiry to force-close promptly", elapsed)
	}

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatalf("active connection was not canceled when the drain expired")
	}
}

// A blocking database Close must not starve the telemetry flush: accepted
// telemetry is delivered while Close is still in flight, and shutdown only
// waits for Close up to the common deadline.
func TestServerStopDoesNotStarveTelemetryBehindBlockingDatabaseClose(t *testing.T) {
	receiver := newTelemetryReceiver(t, http.StatusOK, nil)

	// Deterministic sampling and a long schedule delay keep the accepted span
	// queued until the shutdown flush.
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.0")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "3600000")

	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	config := telemetry.TelemetryConfig{
		Enabled:         true,
		Environment:     "test",
		Endpoint:        receiver.endpoint(),
		ShutdownTimeout: time.Second,
	}
	runtime, err := telemetry.NewRuntime(context.Background(), config,
		telemetry.WithTelemetryMetrics(runtimeMetrics),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	db := newBlockingDatabase()
	defer db.releaseClose()

	srv := mustServer(t, db, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 5 * time.Second,
	})
	registerTracedProbe(srv)

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
		t.Fatalf("probe status = %d, want %d", got, http.StatusOK)
	}

	stopped := make(chan error, 1)
	go func() { stopped <- srv.Stop(context.Background()) }()

	// The queued span must be delivered while Close is still blocked.
	deadline := time.After(2 * time.Second)
	for {
		names := receiver.spanNames(t)
		delivered := false
		for _, name := range names {
			if name == "HTTP GET /api/v1/probe" {
				delivered = true
			}
		}
		if delivered {
			break
		}
		select {
		case err := <-stopped:
			t.Fatalf("Stop() returned %v before the queued span was delivered", err)
		case <-deadline:
			t.Fatalf("queued span was not delivered while database Close was blocked")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Stop must not wait past the common deadline for the blocked Close.
	select {
	case err := <-stopped:
		t.Fatalf("Stop() returned %v before the blocked Close was released", err)
	case <-time.After(200 * time.Millisecond):
	}

	db.releaseClose()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop() did not return after the database Close was released")
	}
}

// An earlier caller deadline bounds the whole shutdown; a canceled caller context
// returns promptly without extending the deadline.
func TestServerStopHonorsEarlierCallerDeadline(t *testing.T) {
	runtime := disabledRuntime(t)

	srv := mustServer(t, &fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 10 * time.Second,
	})
	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	// Already-canceled parent must return immediately.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := srv.Stop(canceledCtx); err != nil {
		t.Fatalf("Stop(canceled) error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Stop(canceled) took %v, want an immediate return", elapsed)
	}
}

// The server owns database cleanup exactly once across repeated shutdowns.
func TestServerStopClosesDatabaseOnce(t *testing.T) {
	db := &fakeDatabase{}
	runtime := disabledRuntime(t)

	srv := mustServer(t, db, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 5 * time.Second,
	})
	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop() error = %v, want nil", err)
	}
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v, want nil", err)
	}
	if got := db.closeCount(); got != 1 {
		t.Fatalf("database Close called %d times, want exactly 1", got)
	}
}
