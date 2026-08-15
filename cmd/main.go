package main

import (
	"context"
	"gin-product-service/cmd/server"
	"gin-product-service/internal/infrastructure/database"
	"log"
)

// @title           Gin Product Service API
// @version         1.0
// @description     REST API for Product & Category Service
// @host            localhost:8080
// @BasePath        /api/v1

func main() {

	ctx := context.Background()
	// ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// defer stop()

	db, err := database.NewPool(ctx)
	if err != nil {
		log.Fatal(err)
	}

	defer db.Close()

	server := server.NewServer(db)
	server.Start()
}
