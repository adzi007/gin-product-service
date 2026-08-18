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

func (router *router) SetupRouter(categoryHandler *handler.CategoryHandler, productHandler *handler.ProductHandler, infraCheckerUseCase domain.InfraCheckUseCase) *gin.Engine {

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
		}
	}

	return r
}
