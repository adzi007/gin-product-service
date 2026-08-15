package database

import "github.com/jackc/pgx/v5/pgxpool"

type Database interface {
	GetDb() *pgxpool.Pool
	Close() *pgxpool.Pool
}
