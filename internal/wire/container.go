package wire

import (
	"gin-product-service/internal/app/category"
	"gin-product-service/internal/app/infrachecker"
	"gin-product-service/internal/app/inventory"
	"gin-product-service/internal/app/product"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/repository"
)

type Container struct {
	CategoryHandler     *handler.CategoryHandler
	ProductHandler      *handler.ProductHandler
	InventoryHandler    *handler.InventoryHandler
	InfraCheckerUseCase domain.InfraCheckUseCase
	// ReservationExpiryUC backs the background reservation-expiration worker
	// (spec Section 18); it is not exposed over HTTP.
	ReservationExpiryUC domain.ReservationExpiryUseCase
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
	productUpdateUC := product.NewProductUpdateUseCase(productRepo)
	productDeleteUC := product.NewProductDeleteUseCase(productRepo)
	optionUC := product.NewOptionUseCase(productRepo, productRepo)
	variantUC := product.NewVariantUseCase(productRepo, productRepo, productRepo)
	mediaUC := product.NewMediaUseCase(productRepo, productRepo, productRepo)
	productHandler := handler.NewProductHandler(productInsertUC, productQueryUC, productUpdateUC, productDeleteUC, optionUC, variantUC, mediaUC)

	// inventory module
	inventoryRepo := repository.NewInventoryRepo(db)
	stockMoveUC := inventory.NewStockMoveUseCase(inventoryRepo)
	reservationUC := inventory.NewReservationUseCase(inventoryRepo)
	reservationExpiryUC := inventory.NewReservationExpiryUseCase(inventoryRepo)
	inventoryHandler := handler.NewInventoryHandler(stockMoveUC, reservationUC)

	healthRepo := repository.NewHealthRepo(db)
	infraCheckerUC := infrachecker.NewInfraCheckerUseCase(healthRepo)

	return &Container{
		CategoryHandler:     categoryHandler,
		ProductHandler:      productHandler,
		InventoryHandler:    inventoryHandler,
		InfraCheckerUseCase: infraCheckerUC,
		ReservationExpiryUC: reservationExpiryUC,
	}
}
