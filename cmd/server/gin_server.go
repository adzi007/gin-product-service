package server

import (
	"context"
	"errors"
	"fmt"
	apphttp "gin-product-service/internal/delivery/http"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/redis"
	"gin-product-service/internal/infrastructure/telemetry"
	"gin-product-service/internal/lifecycle"
	"gin-product-service/internal/wire"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// defaultListenAddress matches the documented service port.
const defaultListenAddress = ":5000"

type ginServer struct {
	app       *gin.Engine
	db        database.Database
	telemetry *telemetry.TelemetryRuntime

	address        string
	shutdownBudget time.Duration
	stop           <-chan struct{}
	report         func(error)

	// mu guards the bound listener and its HTTP server, which Listen publishes
	// for Addr and Stop.
	mu         sync.Mutex
	httpServer *http.Server
	listener   net.Listener

	// ready is closed once the listener is bound.
	ready     chan struct{}
	readyOnce sync.Once

	shutdownOnce sync.Once
	shutdownErr  error
}

// Options configure the server lifecycle. Zero values fall back to the
// production defaults, so the process entry point only sets what it needs.
type Options struct {
	// Address is the listen address; "host:0" binds an ephemeral port.
	Address string
	// ShutdownBudget bounds the whole shutdown sequence.
	ShutdownBudget time.Duration
	// Stop triggers shutdown when closed. When nil, SIGINT/SIGTERM are observed.
	Stop <-chan struct{}
	// Report receives sanitized lifecycle errors instead of the default logger.
	Report func(error)
}

// NewServer builds the production server, rejecting a nil telemetry runtime.
func NewServer(db database.Database, tracing *telemetry.TelemetryRuntime) (AppServer, error) {
	srv, err := NewServerWithOptions(db, tracing, Options{})
	if err != nil {
		return nil, err
	}
	return srv, nil
}

// NewServerWithOptions builds the server and wires every route up front, so the
// engine is complete before shutdown behavior is exercised. A nil telemetry
// runtime is rejected so instrumentation is never attached implicitly.
func NewServerWithOptions(db database.Database, tracing *telemetry.TelemetryRuntime, opts Options) (*ginServer, error) {
	if tracing == nil {
		return nil, errors.New("telemetry runtime is required")
	}

	address := opts.Address
	if address == "" {
		address = defaultListenAddress
	}
	budget := opts.ShutdownBudget
	if budget <= 0 {
		budget = lifecycle.ServiceShutdownBudget
	}

	s := &ginServer{
		app:            gin.Default(),
		db:             db,
		telemetry:      tracing,
		address:        address,
		shutdownBudget: budget,
		stop:           opts.Stop,
		report:         opts.Report,
		ready:          make(chan struct{}),
	}
	s.setupRoutes()
	return s, nil
}

// setupRoutes is the composition step: infrastructure adapters and use cases are
// built with the explicit telemetry runtime and registered on the engine.
func (s *ginServer) setupRoutes() {
	reservationLocker := redis.NewReservationLocker(
		os.Getenv("REDIS_REST_URL"),
		os.Getenv("REDIS_REST_TOKEN"),
		redis.WithTracing(s.telemetry.TracerProvider, s.telemetry.Propagator),
	)

	c := wire.NewContainer(s.db, reservationLocker)
	router := apphttp.NewAppRouter(s.app)
	jwtSecret := os.Getenv("API_JWT_SECRET")
	router.SetupRouter(c.CategoryHandler, c.ProductHandler, c.InfraCheckerUseCase, c.ReviewHandler, c.InventoryHandler, jwtSecret, s.telemetry)

	// expose Prometheus scrape endpoint
	s.app.GET("/metrics", gin.WrapH(promhttp.Handler()))
}

// Handler exposes the configured engine.
func (s *ginServer) Handler() http.Handler { return s.app }

// Router exposes the engine so additional routes can be registered before Listen.
func (s *ginServer) Router() *gin.Engine { return s.app }

// Addr reports the bound address, or the configured address before Listen.
func (s *ginServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.address
}

// Ready is closed once the listener is bound, so callers can wait for startup
// without polling.
func (s *ginServer) Ready() <-chan struct{} { return s.ready }

// Listen binds the configured address and serves in the background. It returns
// once the socket is bound, so callers can observe the actual address.
func (s *ginServer) Listen() error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.address, err)
	}
	httpServer := &http.Server{
		Handler:      s.app,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.mu.Lock()
	s.listener = listener
	s.httpServer = httpServer
	s.mu.Unlock()

	s.readyOnce.Do(func() { close(s.ready) })

	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.reportError(fmt.Errorf("http server stopped: %w", err))
		}
	}()
	return nil
}

// Start binds the listener, waits for the injected stop signal (or an OS
// termination signal), then performs one bounded shutdown.
func (s *ginServer) Start() {
	if err := s.Listen(); err != nil {
		log.Fatal(err)
	}

	<-s.stopSignal()

	if err := s.Stop(context.Background()); err != nil {
		s.reportError(err)
	}
}

// Stop performs the bounded shutdown sequence exactly once: stop accepting new
// work, drain accepted requests within a deadline that reserves time for the
// telemetry flush, close dependencies without starving that flush, and deliver
// accepted telemetry within the remaining deadline. It is idempotent and bounded
// by the configured budget and any earlier caller deadline.
func (s *ginServer) Stop(parent context.Context) error {
	s.shutdownOnce.Do(func() {
		totalCtx, cancel := context.WithTimeout(parent, s.shutdownBudget)
		defer cancel()

		var errs []error

		s.mu.Lock()
		httpServer := s.httpServer
		listener := s.listener
		s.mu.Unlock()

		// Reserve a positive telemetry delivery slice inside the one total
		// deadline so a slow HTTP drain cannot starve the flush. Disabled
		// telemetry reserves nothing.
		reservation := time.Duration(0)
		if s.telemetry != nil {
			reservation = s.telemetry.EffectiveShutdownTimeout()
		}

		// The drain window ends before the total deadline, reserving time for
		// telemetry. It never extends the common deadline or a caller deadline.
		drainBudget := s.shutdownBudget
		if deadline, ok := totalCtx.Deadline(); ok {
			drainBudget = time.Until(deadline) - reservation
			if drainBudget < 0 {
				drainBudget = 0
			}
		}
		drainCtx, drainCancel := context.WithTimeout(totalCtx, drainBudget)
		defer drainCancel()

		// 1. Stop HTTP intake and drain accepted requests.
		if httpServer != nil {
			drainErr := httpServer.Shutdown(drainCtx)
			if drainCtx.Err() != nil {
				// The drain allocation expired: force-close active connections
				// so outstanding work is canceled rather than left hanging. This
				// is the deliberate bounded-drain path, not a shutdown failure.
				_ = httpServer.Close()
			} else if drainErr != nil {
				errs = append(errs, drainErr)
			}
		} else if listener != nil {
			_ = listener.Close()
		}

		// 2. Close dependencies without blocking the telemetry flush.
		dbDone := make(chan struct{})
		if s.db != nil {
			go func() {
				s.db.Close()
				close(dbDone)
			}()
		}

		// 3. Flush accepted telemetry with the still-live total context, never
		// the expired drain context.
		if s.telemetry != nil {
			if err := s.telemetry.Shutdown(totalCtx); err != nil {
				errs = append(errs, err)
			}
		}

		// 4. Wait for dependency close only until the common deadline.
		if s.db != nil {
			select {
			case <-dbDone:
			case <-totalCtx.Done():
			}
		}

		s.shutdownErr = errors.Join(errs...)
	})
	return s.shutdownErr
}

// Use registers additional engine middleware.
func (s *ginServer) Use(args gin.HandlerFunc) {
	s.app.Use(args)
}

// Close performs a bounded shutdown; it is safe to call more than once.
func (s *ginServer) Close() {
	_ = s.Stop(context.Background())
}

// stopSignal returns the injected trigger or an OS signal channel.
func (s *ginServer) stopSignal() <-chan struct{} {
	if s.stop != nil {
		return s.stop
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		<-quit
		close(done)
	}()
	return done
}

func (s *ginServer) reportError(err error) {
	if err == nil {
		return
	}
	if s.report != nil {
		s.report(err)
		return
	}
	log.Printf("server: %v", err)
}
