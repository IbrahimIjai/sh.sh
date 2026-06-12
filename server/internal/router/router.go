package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/handlers"
)

func SetupRouter(authHandler http.Handler) *gin.Engine {
	r := gin.New()

	// Middleware
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Health route
	r.GET("/healthz", handlers.HealthCheck)

	if authHandler != nil {
		r.Any("/auth", gin.WrapH(authHandler))
		r.Any("/auth/*path", gin.WrapH(authHandler))
	}

	// API v1 routes
	v1 := r.Group("/api/v1")
	{
		v1.POST("/users", handlers.CreateUser)
	}

	return r
}
