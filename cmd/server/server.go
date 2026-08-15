package server

import "github.com/gin-gonic/gin"

type AppServer interface {
	Start()
	Use(gin.HandlerFunc)
	Close()
}
