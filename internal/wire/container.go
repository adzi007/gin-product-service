package wire

import (
	"gin-product-service/internal/app/category"
	"gin-product-service/internal/app/infrachecker"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/repository"
)

type Container struct {
	CategoryHandler     *handler.CategoryHandler
	InfraCheckerUseCase domain.InfraCheckUseCase
	// ProductHandler *handler.ProductHandler
	// etc, as they're added
}

func NewContainer(db database.Database) *Container {
	// category module
	categoryRepo := repository.NewCategoryRepo(db)
	categoryQueryUC := category.NewCategoryQueryUseCase(categoryRepo)
	categoryInsertUC := category.NewCategoryInsertUseCase(categoryRepo)
	categoryUpdateUC := category.NewCategoryUpdateUseCase(categoryRepo)
	categoryDeleteUC := category.NewCategoryDeleteUseCase(categoryRepo)
	categoryHandler := handler.NewCategoryHandler(categoryQueryUC, categoryInsertUC, categoryUpdateUC, categoryDeleteUC)

	healthRepo := repository.NewHealthRepo(db)
	infraCheckerUC := infrachecker.NewInfraCheckerUseCase(healthRepo)

	return &Container{
		CategoryHandler:     categoryHandler,
		InfraCheckerUseCase: infraCheckerUC,
	}
}
