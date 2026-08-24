package server

import (
	"context"
	"fmt"
	apphttp "gin-product-service/internal/delivery/http"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/wire"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type ginServer struct {
	app *gin.Engine
	db  database.Database
	// conn     *grpc.ClientConn
	// rabbitMQ *rabbitmq.RabbitMQ
}

func NewServer(db database.Database) AppServer {

	server := gin.Default()

	return &ginServer{
		app: server,
		db:  db,
	}

}

func (s *ginServer) Start() {

	c := wire.NewContainer(s.db)
	router := apphttp.NewAppRouter(s.app)
	router.SetupRouter(c.CategoryHandler, c.ProductHandler, c.InventoryHandler, c.InfraCheckerUseCase)

	// expose Prometheus scrape endpoint
	s.app.GET("/metrics", gin.WrapH(promhttp.Handler()))

	srv := &http.Server{
		Addr:         ":5000",
		Handler:      s.app,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// if err := s.app.Run(":5000"); err != nil {
	// 	log.Fatalf("Failed to start server: %v", err)
	// }
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Reservation expiration worker (spec Section 18). It runs on a ticker
	// until the process receives a shutdown signal, then drains cleanly so no
	// goroutine leaks.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go startReservationExpiryWorker(workerCtx, c.ReservationExpiryUC, workerDone)

	<-quit

	// Stop the worker, allowing the current ExpireDue run to finish.
	workerCancel()
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		log.Printf("reservation expiry worker did not stop in time")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("forced shutdown: %v", err)
	}
	s.db.Close()

}
func (s *ginServer) Use(args gin.HandlerFunc) {
	s.app.Use(args)
}
func (s *ginServer) Close() {
	fmt.Println("close connection...")
}

// reservationExpiryInterval controls how often the reservation-expiration
// worker scans for due reservations (spec Section 18).
const reservationExpiryInterval = time.Minute

// startReservationExpiryWorker runs ReservationExpiryUseCase.ExpireDue on a
// ticker until ctx is cancelled, then signals workerDone. It runs once
// immediately on startup so stale reservations are not left to linger.
func startReservationExpiryWorker(ctx context.Context, uc domain.ReservationExpiryUseCase, workerDone chan<- struct{}) {
	defer close(workerDone)

	ticker := time.NewTicker(reservationExpiryInterval)
	defer ticker.Stop()

	runExpiryOnce(ctx, uc)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runExpiryOnce(ctx, uc)
		}
	}
}

// runExpiryOnce executes a single expiration pass, logging failures and the
// number of reservations expired. It returns early if the context is already
// cancelled (during shutdown).
func runExpiryOnce(ctx context.Context, uc domain.ReservationExpiryUseCase) {
	if ctx.Err() != nil {
		return
	}
	expired, err := uc.ExpireDue(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("reservation expiry worker error: %v", err)
		return
	}
	if expired > 0 {
		log.Printf("reservation expiry worker expired %d reservation(s)", expired)
	}
}
