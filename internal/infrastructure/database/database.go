package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TxFunc is a unit of work executed inside a single database transaction.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

type Database interface {
	GetDb() *pgxpool.Pool
	// WithTx runs fn inside a database transaction: it commits when fn returns
	// nil and rolls back otherwise. Used by use cases that must orchestrate
	// several repository statements atomically (see specs/inventory-reservation.md
	// Section 3.2).
	WithTx(ctx context.Context, fn TxFunc) error
	Close()
}
