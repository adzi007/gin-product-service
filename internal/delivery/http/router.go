package http

import (
	_ "gin-product-service/docs"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/delivery/http/middleware"
	adminmw "gin-product-service/internal/delivery/http/middleware/admin"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/telemetry"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

type router struct {
	appServer *gin.Engine
}

func NewAppRouter(app *gin.Engine) router {
	return router{
		appServer: app,
	}
}

// SetupRouter registers every route and its explicit access policy.
//
// Management routes are protected by the Auth0 admin chain
// admin.RequireAuth(verifier) followed by admin.RequirePermission("<grant>"), in
// that order, so authentication always precedes authorization and both abort
// before the business handler. The chain is declared per route rather than on a
// group so public catalog reads and customer review routes never inherit it.
func (router *router) SetupRouter(categoryHandler *handler.CategoryHandler, productHandler *handler.ProductHandler, infraCheckerUseCase domain.InfraCheckUseCase, reviewHandler *handler.ReviewHandler, inventoryHandler *handler.InventoryHandler, adminVerifier domain.AdminTokenVerifier, jwtSecret string, runtime *telemetry.TelemetryRuntime) *gin.Engine {

	r := router.appServer

	// Inbound tracing is registered before authentication and every handler so the
	// server span covers the whole request. Only /api/v1 business routes and
	// /readyz are traced; health, metrics, Swagger, and unmatched routes are not.
	tracingConfig := middleware.TracingConfig{}
	if runtime != nil {
		tracingConfig.Enabled = runtime.Enabled
		tracingConfig.TracerProvider = runtime.TracerProvider
		tracingConfig.Propagator = runtime.Propagator
		// Correlate request-scoped logs with the active server span.
		tracingConfig.EnrichContext = logger.WithTraceContext
	}
	r.Use(middleware.Tracing(tracingConfig))

	// Tracing-aware recovery is registered inside tracing so a recovered panic can
	// mark the active server span as an error before it closes, while keeping the
	// established 500 response contract.
	r.Use(middleware.Recovery(middleware.RecoveryConfig{
		Report: func(c *gin.Context) {
			logger.L(c.Request.Context()).Error("recovered from panic")
		},
	}))

	// 1. Redirect /swagger to /swagger/index.html
	r.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/swagger/index.html")
	})

	// 2. Wildcard route for serving static Swagger UI assets
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.GET("/readyz", func(c *gin.Context) {
		if err := infraCheckerUseCase.CheckDatabase(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	// adminAuth authenticates one Auth0 RS256 admin credential. Each management
	// route then requires its own exact permission.
	adminAuth := adminmw.RequireAuth(adminVerifier)

	v1 := r.Group("/api/v1")
	{
		categories := v1.Group("/categories")
		{
			categories.POST("", adminAuth, adminmw.RequirePermission("categories:create"), categoryHandler.Create)
			// Public catalog read: no token is required or validated.
			categories.GET("", categoryHandler.Fetch)
			// Dropdown must be declared before /:id to avoid shadowing.
			categories.GET("/dropdown", categoryHandler.Dropdown)
			categories.GET("/:id", categoryHandler.GetByID)
			categories.PUT("/:id", adminAuth, adminmw.RequirePermission("categories:update"), categoryHandler.Update)
			categories.DELETE("/:id", adminAuth, adminmw.RequirePermission("categories:delete"), categoryHandler.Delete)
		}

		products := v1.Group("/products")
		{
			products.POST("", adminAuth, adminmw.RequirePermission("products:create"), productHandler.Create)
			// Public catalog read: no token is required or validated.
			products.GET("", productHandler.Fetch)
			// /:id also serves handle lookups — see ProductHandler.GetByID.
			products.GET("/:id", productHandler.GetByID)
			products.PATCH("/:id", adminAuth, adminmw.RequirePermission("products:update"), productHandler.Update)
			products.DELETE("/:id", adminAuth, adminmw.RequirePermission("products:delete"), productHandler.Archive)
			products.POST("/:id/restore", adminAuth, adminmw.RequirePermission("products:restore"), productHandler.Restore)
			products.DELETE("/:id/purge", adminAuth, adminmw.RequirePermission("products:purge"), productHandler.Purge)

			products.POST("/:id/options", adminAuth, adminmw.RequirePermission("products:options:create"), productHandler.CreateOption)
			// Static /reorder must be registered before the /:option_id
			// wildcard so Gin resolves it to ReorderOptions, not RenameOption.
			products.PATCH("/:id/options/reorder", adminAuth, adminmw.RequirePermission("products:options:update"), productHandler.ReorderOptions)
			products.PATCH("/:id/options/:option_id", adminAuth, adminmw.RequirePermission("products:options:update"), productHandler.RenameOption)
			products.DELETE("/:id/options/:option_id", adminAuth, adminmw.RequirePermission("products:options:delete"), productHandler.DeleteOption)
			products.POST("/:id/options/:option_id/values", adminAuth, adminmw.RequirePermission("products:options:create"), productHandler.AddOptionValue)
			products.PATCH("/:id/options/:option_id/values/:value_id", adminAuth, adminmw.RequirePermission("products:options:update"), productHandler.UpdateOptionValue)
			products.DELETE("/:id/options/:option_id/values/:value_id", adminAuth, adminmw.RequirePermission("products:options:delete"), productHandler.DeleteOptionValue)

			products.POST("/:id/variants", adminAuth, adminmw.RequirePermission("products:variants:create"), productHandler.CreateVariant)
			products.POST("/:id/variants/bulk", adminAuth, adminmw.RequirePermission("products:variants:create"), productHandler.BulkCreateVariants)
			products.PATCH("/:id/variants/bulk", adminAuth, adminmw.RequirePermission("products:variants:update"), productHandler.BulkUpdateVariants)
			products.POST("/:id/variants/bulk-delete", adminAuth, adminmw.RequirePermission("products:variants:delete"), productHandler.BulkDeleteVariants)
			products.PATCH("/:id/variants/reorder", adminAuth, adminmw.RequirePermission("products:variants:update"), productHandler.ReorderVariants)

			products.POST("/:id/media", adminAuth, adminmw.RequirePermission("products:media:create"), productHandler.CreateMedia)
			// Static /reorder must be registered before the /:media_id wildcard
			// so Gin resolves it to ReorderMedia, not UpdateMedia.
			products.PATCH("/:id/media/reorder", adminAuth, adminmw.RequirePermission("products:media:update"), productHandler.ReorderMedia)
			products.PATCH("/:id/media/:media_id", adminAuth, adminmw.RequirePermission("products:media:update"), productHandler.UpdateMedia)
			products.DELETE("/:id/media/:media_id", adminAuth, adminmw.RequirePermission("products:media:delete"), productHandler.DeleteMedia)

			products.GET("/:id/reviews", adminAuth, adminmw.RequirePermission("products:reviews:read"), reviewHandler.Fetch)
			products.GET("/:id/reviews/summary", adminAuth, adminmw.RequirePermission("products:reviews:read"), reviewHandler.Summary)
			// Customer review creation keeps the existing HS256 identity path.
			products.POST("/:id/reviews", middleware.RequireAuth(jwtSecret), reviewHandler.Create)
		}

		reviews := v1.Group("/reviews")
		{
			reviews.GET("/:reviewId", adminAuth, adminmw.RequirePermission("reviews:read"), reviewHandler.GetByID)
			// Customer-owned review edits keep the existing HS256 identity path.
			reviews.PATCH("/:reviewId", middleware.RequireAuth(jwtSecret), reviewHandler.Update)
			reviews.DELETE("/:reviewId", middleware.RequireAuth(jwtSecret), reviewHandler.Delete)
		}

		users := v1.Group("/users")
		{
			users.GET("/me/reviews", middleware.RequireAuth(jwtSecret), reviewHandler.FetchMine)
		}

		variants := v1.Group("/variants")
		{
			variants.PATCH("/:id", adminAuth, adminmw.RequirePermission("variants:update"), productHandler.UpdateVariant)
			variants.DELETE("/:id", adminAuth, adminmw.RequirePermission("variants:delete"), productHandler.DeleteVariant)
			variants.POST("/:id/restore", adminAuth, adminmw.RequirePermission("variants:restore"), productHandler.RestoreVariant)

			variants.POST("/:id/media", adminAuth, adminmw.RequirePermission("variants:media:create"), productHandler.AttachVariantMedia)
			// Static /reorder must be registered before /:media_id.
			variants.PATCH("/:id/media/reorder", adminAuth, adminmw.RequirePermission("variants:media:update"), productHandler.ReorderVariantMedia)
			variants.DELETE("/:id/media/:media_id", adminAuth, adminmw.RequirePermission("variants:media:delete"), productHandler.DetachVariantMedia)
		}

		inventory := v1.Group("/inventory")
		{
			inventory.POST("/reservations", adminAuth, adminmw.RequirePermission("inventory:reservations:create"), inventoryHandler.CreateReservation)
		}
	}

	return r
}
