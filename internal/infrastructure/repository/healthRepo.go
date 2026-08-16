package repository

import (
	"context"
	"gin-product-service/internal/infrastructure/database"
)

type healthRepo struct {
	db database.Database
}

func NewHealthRepo(db database.Database) *healthRepo {
	return &healthRepo{db: db}
}

func (r *healthRepo) PingDb(ctx context.Context) error {
	return r.db.GetDb().Ping(ctx)
}
