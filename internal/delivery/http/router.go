package http

import (
	// "gin-product-service/internal/delivery/http/handler"

	_ "gin-product-service/docs"
	"gin-product-service/internal/delivery/http/handler"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// func SetupRouter(categoryHandler *handler.CategoryHandler) *gin.Engine {
func SetupRouter(categoryHandler *handler.CategoryHandler) *gin.Engine {
	r := gin.Default()

	// 1. Redirect /swagger to /swagger/index.html
	r.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/swagger/index.html")
	})

	// 2. Wildcard route for serving static Swagger UI assets
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	v1 := r.Group("/api/v1")
	{
		categories := v1.Group("/categories")
		{
			// categories.POST("", categoryHandler.Create)
			categories.GET("", categoryHandler.Fetch)
			// categories.GET("/:id", categoryHandler.GetByID)
			// categories.PUT("/:id", categoryHandler.Update)
			// categories.DELETE("/:id", categoryHandler.Delete)
		}
	}

	return r
}
