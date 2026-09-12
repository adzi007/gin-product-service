package wire

import (
	"gin-product-service/internal/app/category"
	"gin-product-service/internal/app/infrachecker"
	"gin-product-service/internal/app/inventory"
	"gin-product-service/internal/app/product"
	"gin-product-service/internal/app/review"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/repository"
	"gin-product-service/internal/infrastructure/telemetry"
)

type Container struct {
	CategoryHandler     *handler.CategoryHandler
	ProductHandler      *handler.ProductHandler
	ReviewHandler       *handler.ReviewHandler
	InventoryHandler    *handler.InventoryHandler
	InfraCheckerUseCase domain.InfraCheckUseCase
}

// NewContainer is the composition root. Every handler-facing use case is wrapped
// with its telemetry decorator here — the only place application
// implementations are decorated — so no domain entity, business rule, or
// application implementation depends on observability.
func NewContainer(db database.Database, reservationLocker domain.ReservationLocker, runtime *telemetry.TelemetryRuntime) *Container {
	decorators := telemetry.DecoratorConfig{
		TracerProvider: runtime.TracerProvider,
		// Refresh the request-scoped logger from each child span's context so
		// application logs carry the current trace_id/span_id.
		EnrichContext: logger.WithTraceContext,
	}

	// category module
	categoryRepo := repository.NewCategoryRepo(db)
	categoryQueryUC := telemetry.NewCategoryQueryDecorator(category.NewCategoryQueryUseCase(categoryRepo), decorators)
	categoryInsertUC := telemetry.NewCategoryInsertDecorator(category.NewCategoryInsertUseCase(categoryRepo), decorators)
	categoryUpdateUC := telemetry.NewCategoryUpdateDecorator(category.NewCategoryUpdateUseCase(categoryRepo), decorators)
	categoryDeleteUC := telemetry.NewCategoryDeleteDecorator(category.NewCategoryDeleteUseCase(categoryRepo), decorators)
	categoryHandler := handler.NewCategoryHandler(categoryQueryUC, categoryInsertUC, categoryUpdateUC, categoryDeleteUC)

	// product module
	productRepo := repository.NewProductRepo(db)
	productInsertUC := telemetry.NewProductInsertDecorator(product.NewProductInsertUseCase(productRepo), decorators)
	productQueryUC := telemetry.NewProductQueryDecorator(product.NewProductQueryUseCase(productRepo), decorators)
	productUpdateUC := telemetry.NewProductUpdateDecorator(product.NewProductUpdateUseCase(productRepo), decorators)
	productDeleteUC := telemetry.NewProductDeleteDecorator(product.NewProductDeleteUseCase(productRepo), decorators)
	optionUC := telemetry.NewOptionDecorator(product.NewOptionUseCase(productRepo, productRepo), decorators)
	variantUC := telemetry.NewVariantDecorator(product.NewVariantUseCase(productRepo, productRepo, productRepo), decorators)
	mediaUC := telemetry.NewMediaDecorator(product.NewMediaUseCase(productRepo, productRepo, productRepo), decorators)
	productHandler := handler.NewProductHandler(productInsertUC, productQueryUC, productUpdateUC, productDeleteUC, optionUC, variantUC, mediaUC)

	healthRepo := repository.NewHealthRepo(db)
	infraCheckerUC := telemetry.NewInfraCheckDecorator(infrachecker.NewInfraCheckerUseCase(healthRepo), decorators)

	// review module
	reviewRepo := repository.NewReviewRepo(db)
	reviewInsertUC := telemetry.NewReviewInsertDecorator(review.NewReviewInsertUseCase(reviewRepo), decorators)
	reviewQueryUC := telemetry.NewReviewQueryDecorator(review.NewReviewQueryUseCase(reviewRepo), decorators)
	reviewUpdateUC := telemetry.NewReviewUpdateDecorator(review.NewReviewUpdateUseCase(reviewRepo), decorators)
	reviewDeleteUC := telemetry.NewReviewDeleteDecorator(review.NewReviewDeleteUseCase(reviewRepo), decorators)
	reviewSummaryUC := telemetry.NewReviewSummaryDecorator(review.NewReviewSummaryUseCase(reviewRepo), decorators)
	reviewHandler := handler.NewReviewHandler(reviewInsertUC, reviewQueryUC, reviewUpdateUC, reviewDeleteUC, reviewSummaryUC)

	// inventory module
	inventoryRepo := repository.NewInventoryRepo(db)
	reservationUC := telemetry.NewReservationDecorator(inventory.NewCreateReservationUseCase(inventoryRepo, reservationLocker), decorators)
	inventoryHandler := handler.NewInventoryHandler(reservationUC)

	return &Container{
		CategoryHandler:     categoryHandler,
		ProductHandler:      productHandler,
		ReviewHandler:       reviewHandler,
		InventoryHandler:    inventoryHandler,
		InfraCheckerUseCase: infraCheckerUC,
	}
}
