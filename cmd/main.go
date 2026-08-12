package main

import (
	"gin-product-service/internal/delivery/http"
	"log"
)

func main() {

	r := http.SetupRouter()

	log.Println("Server running on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
