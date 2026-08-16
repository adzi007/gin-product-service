package domain

import "context"

type InfraCheckUseCase interface {
	CheckDatabase(ctx context.Context) error
}

type HealthRepository interface {
	PingDb(ctx context.Context) error
}
