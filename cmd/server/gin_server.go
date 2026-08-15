package server

import (
	"fmt"
	"gin-product-service/internal/delivery/http"
	"gin-product-service/internal/infrastructure/database"
	"gin-product-service/internal/wire"
	"log"

	"github.com/gin-gonic/gin"
)

type ginServer struct {
	app *gin.Engine
	db  database.Database
	// conn     *grpc.ClientConn
	// rabbitMQ *rabbitmq.RabbitMQ
}

func NewServer(db database.Database) AppServer {

	server := gin.Default()

	return &ginServer{
		app: server,
		db:  db,
	}

}

func (s *ginServer) Start() {

	c := wire.NewContainer(s.db)

	// categoryHandler := handler.NewCategoryHandler(c)
	// s.app = http.SetupRouter(categoryHandler)

	router := http.NewAppRouter(s.app)
	router.SetupRouter(c.CategoryHandler)

	if err := s.app.Run(":5000"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

}
func (s *ginServer) Use(args gin.HandlerFunc) {
	s.app.Use(args)
}
func (s *ginServer) Close() {
	fmt.Println("close connection...")
}
