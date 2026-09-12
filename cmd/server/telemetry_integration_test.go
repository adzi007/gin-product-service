package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/metrics"
	"gin-product-service/internal/infrastructure/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// telemetryReceiver is a local OTLP/HTTP capture receiver. It exists only for
// the duration of the test and retains nothing.
type telemetryReceiver struct {
	server *httptest.Server
	status int
	hang   chan struct{}

	mu       sync.Mutex
	requests []capturedOTLP
}

type capturedOTLP struct {
	path        string
	contentType string
	body        []byte
}

func newTelemetryReceiver(t *testing.T, status int, hang chan struct{}) *telemetryReceiver {
	t.Helper()

	receiver := &telemetryReceiver{status: status, hang: hang}
	receiver.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		receiver.mu.Lock()
		receiver.requests = append(receiver.requests, capturedOTLP{
			path:        r.URL.Path,
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		})
		receiver.mu.Unlock()

		if receiver.hang != nil {
			select {
			case <-receiver.hang:
			case <-r.Context().Done():
			}
		}

		payload, _ := proto.Marshal(&coltracepb.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(receiver.status)
		_, _ = w.Write(payload)
	}))

	t.Cleanup(func() {
		if hang != nil {
			select {
			case <-hang:
			default:
				close(hang)
			}
		}
		receiver.server.Close()
	})

	return receiver
}

func (r *telemetryReceiver) endpoint() string { return r.server.URL + "/v1/traces" }

func (r *telemetryReceiver) snapshot() []capturedOTLP {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capturedOTLP, len(r.requests))
	copy(out, r.requests)
	return out
}

func (r *telemetryReceiver) spanNames(t *testing.T) []string {
	t.Helper()

	var names []string
	for _, request := range r.snapshot() {
		message := &coltracepb.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(request.body, message); err != nil {
			t.Fatalf("decode OTLP request: %v", err)
		}
		for _, resourceSpans := range message.GetResourceSpans() {
			for _, scopeSpans := range resourceSpans.GetScopeSpans() {
				for _, span := range scopeSpans.GetSpans() {
					names = append(names, span.GetName())
				}
			}
		}
	}
	return names
}

// registerTracedProbe registers a business route whose application operation is
// decorated exactly like the production composition root does.
func registerTracedProbe(srv *ginServer, runtime *telemetry.TelemetryRuntime) {
	decorated := telemetry.NewCategoryQueryDecorator(probeCategoryQuery{}, telemetry.DecoratorConfig{
		TracerProvider: runtime.TracerProvider,
		EnrichContext:  logger.WithTraceContext,
	})

	srv.Router().GET("/api/v1/probe", func(c *gin.Context) {
		if _, err := decorated.FindAll(c.Request.Context(), domain.ListCategoryParams{}); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
}

type probeCategoryQuery struct{}

func (probeCategoryQuery) FindAll(context.Context, domain.ListCategoryParams) (domain.PaginatedCategories, error) {
	return domain.PaginatedCategories{}, nil
}

func (probeCategoryQuery) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	return nil, nil
}

func (probeCategoryQuery) GetByID(context.Context, int) (domain.Category, error) {
	return domain.Category{}, nil
}

// Enabled operation: a completed request produces one connected trace that
// reaches the local capture receiver through the real OTLP/HTTP boundary.
func TestServerEnabledExportsRealOTLPRequest(t *testing.T) {
	receiver := newTelemetryReceiver(t, http.StatusOK, nil)
	runtime, _ := enabledRuntime(t, receiver.endpoint(), 2*time.Second)

	srv := NewServerWithOptions(&fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 8 * time.Second,
	})
	registerTracedProbe(srv, runtime)

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	requests := receiver.snapshot()
	if len(requests) == 0 {
		t.Fatalf("no OTLP request reached the capture receiver")
	}
	for _, request := range requests {
		if request.path != "/v1/traces" {
			t.Errorf("path = %q, want /v1/traces", request.path)
		}
		if !strings.Contains(request.contentType, "application/x-protobuf") {
			t.Errorf("Content-Type = %q, want the OTLP protobuf content type", request.contentType)
		}
	}

	names := receiver.spanNames(t)
	if len(names) == 0 {
		t.Fatalf("captured payload contained no spans")
	}

	var sawServer, sawApplication bool
	for _, name := range names {
		switch name {
		case "HTTP GET /api/v1/probe":
			sawServer = true
		case "app.category.query.find_all":
			sawApplication = true
		}
	}
	if !sawServer || !sawApplication {
		t.Fatalf("captured spans = %v, want the server span and its application child", names)
	}
}

// Disabled operation: business responses are unchanged and no export is attempted.
func TestServerDisabledAttemptsNoExport(t *testing.T) {
	receiver := newTelemetryReceiver(t, http.StatusOK, nil)
	runtime := disabledRuntime(t)

	srv := NewServerWithOptions(&fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 5 * time.Second,
	})
	srv.Router().GET("/api/v1/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
			t.Fatalf("status = %d, want %d", got, http.StatusOK)
		}
	}
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	if got := len(receiver.snapshot()); got != 0 {
		t.Fatalf("capture receiver requests = %d, want 0 while tracing is disabled", got)
	}
}

// SC-006: a destination outage must not change responses, and the service stays
// responsive for the whole test period.
func TestServerUnavailableDestinationKeepsResponsesUnchanged(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := unavailable.URL + "/v1/traces"
	unavailable.Close()

	runtime, runtimeMetrics := enabledRuntime(t, endpoint, time.Second)

	srv := NewServerWithOptions(&fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 8 * time.Second,
	})
	registerTracedProbe(srv, runtime)

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	requests := 0
	for time.Now().Before(deadline) {
		if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
			t.Fatalf("status = %d, want %d during a destination outage", got, http.StatusOK)
		}
		requests++
	}
	if requests == 0 {
		t.Fatalf("no requests were served during the outage window")
	}

	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil (fail-open)", err)
	}

	failures := testutil.ToFloat64(runtimeMetrics.ExporterFailures.WithLabelValues(string(metrics.ExporterFailureTransport)))
	if failures < 1 {
		t.Fatalf("exporter failure counter = %v, want the outage reported", failures)
	}
}

// Queue pressure drops newly completed spans instead of blocking requests, and
// the drops stay visible on the metrics surface.
func TestServerQueuePressureDoesNotBlockRequests(t *testing.T) {
	hang := make(chan struct{})
	receiver := newTelemetryReceiver(t, http.StatusOK, hang)

	runtimeMetrics := metrics.NewTelemetryMetrics(prometheus.NewRegistry())
	config := telemetry.TelemetryConfig{
		Enabled:            true,
		ServiceName:        "gin-product-service",
		Environment:        "test",
		Endpoint:           receiver.endpoint(),
		Compression:        telemetry.CompressionNone,
		ExporterTimeout:    time.Second,
		RootSampleRatio:    1,
		QueueSize:          2,
		BatchSize:          2,
		ScheduleDelay:      10 * time.Millisecond,
		BatchExportTimeout: time.Second,
		ShutdownTimeout:    time.Second,
	}
	runtime, err := telemetry.NewRuntime(context.Background(), config,
		telemetry.WithTelemetryMetrics(runtimeMetrics),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	srv := NewServerWithOptions(&fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 8 * time.Second,
	})
	registerTracedProbe(srv, runtime)

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	start := time.Now()
	for i := 0; i < 40; i++ {
		if got := get(t, "http://"+srv.Addr()+"/api/v1/probe"); got != http.StatusOK {
			t.Fatalf("status = %d, want %d under queue pressure", got, http.StatusOK)
		}
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("requests took %v under queue pressure; they must not wait for telemetry", elapsed)
	}

	dropped := testutil.ToFloat64(runtimeMetrics.SpansDropped.WithLabelValues(string(metrics.DropReasonQueueFull)))
	if dropped < 1 {
		t.Fatalf("dropped spans = %v, want queue pressure reported", dropped)
	}

	// Release the receiver and shut down; accepted telemetry gets one bounded flush.
	close(hang)
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
}

// SC-009: an injected termination signal drains accepted requests and completes
// shutdown within the service budget.
func TestServerGracefulShutdownWithinBudget(t *testing.T) {
	receiver := newTelemetryReceiver(t, http.StatusOK, nil)
	runtime, _ := enabledRuntime(t, receiver.endpoint(), 2*time.Second)

	stop := make(chan struct{})
	reported := make(chan error, 1)

	srv := NewServerWithOptions(&fakeDatabase{}, runtime, Options{
		Address:        "127.0.0.1:0",
		ShutdownBudget: 5 * time.Second,
		Stop:           stop,
		Report:         func(err error) { reported <- err },
	})

	started := make(chan struct{})
	release := make(chan struct{})
	srv.Router().GET("/api/v1/slow-probe", func(c *gin.Context) {
		close(started)
		<-release
		c.Status(http.StatusOK)
	})

	startDone := make(chan struct{})
	go func() {
		srv.Start()
		close(startDone)
	}()

	// Wait until Start has bound the ephemeral port.
	select {
	case <-srv.Ready():
	case <-time.After(5 * time.Second):
		t.Fatalf("server never bound its listener")
	}
	addr := srv.Addr()

	statuses := make(chan int, 1)
	go func() { statuses <- get(t, "http://"+addr+"/api/v1/slow-probe") }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("accepted request never started")
	}

	shutdownStart := time.Now()
	close(stop)
	close(release)

	select {
	case status := <-statuses:
		if status != http.StatusOK {
			t.Fatalf("drained request status = %d, want %d", status, http.StatusOK)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("accepted request never completed during shutdown")
	}

	select {
	case <-startDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("Start() did not return after the stop signal")
	}

	if elapsed := time.Since(shutdownStart); elapsed > 5*time.Second {
		t.Fatalf("shutdown took %v, want it within the service budget", elapsed)
	}

	select {
	case err := <-reported:
		t.Fatalf("shutdown reported an error: %v", err)
	default:
	}
}
