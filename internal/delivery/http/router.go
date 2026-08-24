package http

import (
	_ "gin-product-service/docs"
	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/domain"
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

func (router *router) SetupRouter(categoryHandler *handler.CategoryHandler, productHandler *handler.ProductHandler, inventoryHandler *handler.InventoryHandler, infraCheckerUseCase domain.InfraCheckUseCase) *gin.Engine {

	r := router.appServer

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

	v1 := r.Group("/api/v1")
	{
		categories := v1.Group("/categories")
		{
			categories.POST("", categoryHandler.Create)
			categories.GET("", categoryHandler.Fetch)
			// Dropdown must be declared before /:id to avoid shadowing.
			categories.GET("/dropdown", categoryHandler.Dropdown)
			categories.GET("/:id", categoryHandler.GetByID)
			categories.PUT("/:id", categoryHandler.Update)
			categories.DELETE("/:id", categoryHandler.Delete)
		}

		products := v1.Group("/products")
		{
			products.POST("", productHandler.Create)
			products.GET("", productHandler.Fetch)
			// /:id also serves handle lookups — see ProductHandler.GetByID.
			products.GET("/:id", productHandler.GetByID)
			products.PATCH("/:id", productHandler.Update)
			products.DELETE("/:id", productHandler.Archive)
			products.POST("/:id/restore", productHandler.Restore)
			products.DELETE("/:id/purge", productHandler.Purge)

			products.POST("/:id/options", productHandler.CreateOption)
			// Static /reorder must be registered before the /:option_id
			// wildcard so Gin resolves it to ReorderOptions, not RenameOption.
			products.PATCH("/:id/options/reorder", productHandler.ReorderOptions)
			products.PATCH("/:id/options/:option_id", productHandler.RenameOption)
			products.DELETE("/:id/options/:option_id", productHandler.DeleteOption)
			products.POST("/:id/options/:option_id/values", productHandler.AddOptionValue)
			products.PATCH("/:id/options/:option_id/values/:value_id", productHandler.UpdateOptionValue)
			products.DELETE("/:id/options/:option_id/values/:value_id", productHandler.DeleteOptionValue)

			products.POST("/:id/variants", productHandler.CreateVariant)
			products.POST("/:id/variants/bulk", productHandler.BulkCreateVariants)
			products.PATCH("/:id/variants/bulk", productHandler.BulkUpdateVariants)
			products.POST("/:id/variants/bulk-delete", productHandler.BulkDeleteVariants)
			products.PATCH("/:id/variants/reorder", productHandler.ReorderVariants)

			products.POST("/:id/media", productHandler.CreateMedia)
			// Static /reorder must be registered before the /:media_id wildcard
			// so Gin resolves it to ReorderMedia, not UpdateMedia.
			products.PATCH("/:id/media/reorder", productHandler.ReorderMedia)
			products.PATCH("/:id/media/:media_id", productHandler.UpdateMedia)
			products.DELETE("/:id/media/:media_id", productHandler.DeleteMedia)
		}

		variants := v1.Group("/variants")
		{
			variants.PATCH("/:id", productHandler.UpdateVariant)
			variants.DELETE("/:id", productHandler.DeleteVariant)
			variants.POST("/:id/restore", productHandler.RestoreVariant)

			variants.POST("/:id/media", productHandler.AttachVariantMedia)
			// Static /reorder must be registered before /:media_id.
			variants.PATCH("/:id/media/reorder", productHandler.ReorderVariantMedia)
			variants.DELETE("/:id/media/:media_id", productHandler.DetachVariantMedia)
		}

		inventory := v1.Group("/inventory")
		{
			inventory.POST("/stock-moves", inventoryHandler.CreateStockMove)
			inventory.POST("/reservations", inventoryHandler.CreateReservation)
			// Static complete/cancel must be registered before the :orderId
			// wildcard (if one were added later); Gin resolves these literally.
			inventory.PUT("/reservations/:orderId/complete", inventoryHandler.CompleteReservation)
			inventory.PUT("/reservations/:orderId/cancel", inventoryHandler.CancelReservation)
		}
	}

	return r
}
