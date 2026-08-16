package server

import (
	"context"
	"fmt"
	apphttp "gin-product-service/internal/delivery/http"
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
	router.SetupRouter(c.CategoryHandler, c.InfraCheckerUseCase)

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
	<-quit

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
