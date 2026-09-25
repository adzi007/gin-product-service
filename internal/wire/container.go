package wire

import (
	"gin-product-service/internal/app/category"
	"gin-product-service/internal/app/infrachecker"
	"gin-product-service/internal/app/inventory"
	"gin-product-service/internal/app/product"
	"gin-product-service/internal/app/review"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/auth0"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/infrastructure/repository"
)

type Container struct {
	CategoryHandler     *handler.CategoryHandler
	ProductHandler      *handler.ProductHandler
	ReviewHandler       *handler.ReviewHandler
	InventoryHandler    *handler.InventoryHandler
	InfraCheckerUseCase domain.InfraCheckUseCase
	// AdminTokenVerifier is the single Auth0 admin credential verifier, shared
	// by every protected route through the router's middleware chain. It is
	// exposed as the domain port so delivery never depends on the adapter.
	AdminTokenVerifier domain.AdminTokenVerifier
}

// NewContainer is the composition root. Use cases are wired directly to their
// handlers; application spans are no longer introduced by mirrored decorators.
// The HTTP middleware owns the single server span and request-log correlation,
// so the wire graph imports neither telemetry nor logging solely for tracing.
//
// The Auth0 admin verifier is built once here from already-validated
// configuration and owns the process-local JWKS cache. Construction performs no
// network I/O.
func NewContainer(db database.Database, reservationLocker domain.ReservationLocker, adminConfig auth0.Config) (*Container, error) {
	adminVerifier, err := auth0.NewVerifier(adminConfig)
	if err != nil {
		return nil, err
	}

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

	healthRepo := repository.NewHealthRepo(db)
	infraCheckerUC := infrachecker.NewInfraCheckerUseCase(healthRepo)

	// review module
	reviewRepo := repository.NewReviewRepo(db)
	reviewInsertUC := review.NewReviewInsertUseCase(reviewRepo)
	reviewQueryUC := review.NewReviewQueryUseCase(reviewRepo)
	reviewUpdateUC := review.NewReviewUpdateUseCase(reviewRepo)
	reviewDeleteUC := review.NewReviewDeleteUseCase(reviewRepo)
	reviewSummaryUC := review.NewReviewSummaryUseCase(reviewRepo)
	reviewHandler := handler.NewReviewHandler(reviewInsertUC, reviewQueryUC, reviewUpdateUC, reviewDeleteUC, reviewSummaryUC)

	// inventory module
	inventoryRepo := repository.NewInventoryRepo(db)
	reservationUC := inventory.NewCreateReservationUseCase(inventoryRepo, reservationLocker)
	inventoryHandler := handler.NewInventoryHandler(reservationUC)

	return &Container{
		CategoryHandler:     categoryHandler,
		ProductHandler:      productHandler,
		ReviewHandler:       reviewHandler,
		InventoryHandler:    inventoryHandler,
		InfraCheckerUseCase: infraCheckerUC,
		AdminTokenVerifier:  adminVerifier,
	}, nil
}
