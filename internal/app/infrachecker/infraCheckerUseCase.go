package infrachecker

import (
	"context"
	"gin-product-service/internal/domain"
)

type infraCheckerUseCase struct {
	healthRepo domain.HealthRepository
}

func NewInfraCheckerUseCase(healthRepo domain.HealthRepository) domain.InfraCheckUseCase {
	return &infraCheckerUseCase{healthRepo: healthRepo}
}

func (uc *infraCheckerUseCase) CheckDatabase(ctx context.Context) error {
	return uc.healthRepo.PingDb(ctx)
}
