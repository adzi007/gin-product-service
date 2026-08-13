package main

import (
	"gin-product-service/internal/delivery/http"
	"gin-product-service/internal/delivery/http/handler"
	"log"
)

// @title           Gin Product Service API
// @version         1.0
// @description     REST API for Product & Category Service
// @host            localhost:8080
// @BasePath        /api/v1

func main() {

	categoryHandler := handler.NewCategoryHandler()

	r := http.SetupRouter(categoryHandler)

	log.Println("Server running on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
