package database

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/exaring/otelpgx"
	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"go.opentelemetry.io/otel/trace"
)

type postgresDatabase struct {
	Db *pgxpool.Pool
}

// PoolOption customises pool construction.
type PoolOption func(*poolOptions)

type poolOptions struct {
	tracerProvider trace.TracerProvider
}

// WithTracerProvider attaches OpenTelemetry pgx instrumentation to the pool.
//
// SQL statement text, query parameters, and connection details are excluded, so
// no customer data, credentials, or endpoints can reach exported spans. The
// provider is supplied explicitly by the composition root; nothing is resolved
// from a package global.
func WithTracerProvider(provider trace.TracerProvider) PoolOption {
	return func(o *poolOptions) { o.tracerProvider = provider }
}

func NewPool(ctx context.Context, opts ...PoolOption) (Database, error) {

	_ = godotenv.Load()

	var options poolOptions
	for _, opt := range opts {
		opt(&options)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable is not set")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	if options.tracerProvider != nil {
		config.ConnConfig.Tracer = otelpgx.NewTracer(
			otelpgx.WithTracerProvider(options.tracerProvider),
			// Mechanical sensitive-data boundary: never export statement text,
			// parameters, or connection details.
			otelpgx.WithDisableSQLStatementInAttributes(),
			otelpgx.WithDisableConnectionDetailsInAttributes(),
		)
	}

	// Register the shopspring/decimal codec on every connection so numeric
	// columns (price, weight, quantities) scan directly into decimal.Decimal.
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &postgresDatabase{Db: pool}, nil
}

func (p *postgresDatabase) GetDb() *pgxpool.Pool {
	return p.Db
}

func (p *postgresDatabase) Close() {
	p.Db.Close()
}
