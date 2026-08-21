package wire

import (
	"gin-product-service/internal/app/category"
	"gin-product-service/internal/app/infrachecker"
	"gin-product-service/internal/app/product"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/repository"
)

type Container struct {
	CategoryHandler     *handler.CategoryHandler
	ProductHandler      *handler.ProductHandler
	InfraCheckerUseCase domain.InfraCheckUseCase
}

func NewContainer(db database.Database) *Container {
	// category module
	categoryRepo := repository.NewCategoryRepo(db)
	categoryQueryUC := category.NewCategoryQueryUseCase(categoryRepo)
	categoryInsertUC := category.NewCategoryInsertUseCase(categoryRepo)
	categoryUpdateUC := category.NewCategoryUpdateUseCase(categoryRepo)
	categoryDeleteUC := category.NewCategoryDeleteUseCase(categoryRepo)
	categoryHandler := handler.NewCategoryHandler(categoryQueryUC, categoryInsertUC, categoryUpdateUC, categoryDeleteUC)

	// product module
	productRepo := repository.NewProductRepo(db)
	productInsertUC := product.NewProductInsertUseCase(productRepo)
	productQueryUC := product.NewProductQueryUseCase(productRepo)
	productHandler := handler.NewProductHandler(productInsertUC, productQueryUC)

	healthRepo := repository.NewHealthRepo(db)
	infraCheckerUC := infrachecker.NewInfraCheckerUseCase(healthRepo)

	return &Container{
		CategoryHandler:     categoryHandler,
		ProductHandler:      productHandler,
		InfraCheckerUseCase: infraCheckerUC,
	}
}
