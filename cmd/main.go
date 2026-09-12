package main

import (
	"context"
	"gin-product-service/cmd/server"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/telemetry"
	"log"
	"os"

	"github.com/joho/godotenv"
)

// @title           Gin Product Service API
// @version         1.0
// @description     REST API for Product & Category Service
// @host            localhost:5000
// @BasePath        /api/v1

func main() {

	ctx := context.Background()
	// ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// defer stop()

	// Loaded before anything reads os.Getenv, so .env-only configuration (e.g.
	// telemetry) is visible at startup instead of only once database.NewPool runs.
	_ = godotenv.Load()

	logger.Init(os.Getenv("APP_ENV"))

	// Validated telemetry is initialized before PostgreSQL so enabled-but-invalid
	// configuration fails startup before any resource is opened.
	tracing, err := loadTelemetry(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// A disabled runtime supplies a no-op provider, so pgx instrumentation attaches
	// but produces no spans.
	db, err := database.NewPool(ctx, database.WithTracerProvider(tracing.TracerProvider))
	if err != nil {
		log.Fatal(err)
	}

	defer db.Close()

	server := server.NewServer(db, tracing)
	server.Start()
}

// loadTelemetry is the testable process-startup seam for tracing. It parses and
// validates the operator configuration and builds the runtime, failing with a
// safe field-level error when tracing is enabled but unusable. Construction
// performs no network I/O, so a destination outage never blocks startup.
func loadTelemetry(ctx context.Context) (*telemetry.TelemetryRuntime, error) {
	config, err := telemetry.LoadConfig()
	if err != nil {
		return nil, err
	}
	return telemetry.NewRuntime(ctx, config)
}
