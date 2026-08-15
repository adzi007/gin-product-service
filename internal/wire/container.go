package wire

import (
	"gin-product-service/internal/app/category"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/repository"
)

type Container struct {
	CategoryHandler *handler.CategoryHandler
	// ProductHandler *handler.ProductHandler
	// etc, as they're added
}

func NewContainer(db database.Database) *Container {
	// category module
	categoryRepo := repository.NewCategoryRepo(db)
	categoryUC := category.NewCategoryQueryUseCase(categoryRepo)
	categoryHandler := handler.NewCategoryHandler(categoryUC)

	return &Container{
		CategoryHandler: categoryHandler,
	}
}
