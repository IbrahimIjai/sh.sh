package router

import (
	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/handlers"
)

func SetupRouter(authServer *auth.Server, queries db.Querier) *gin.Engine {
	r := gin.New()
	linkHandler := handlers.NewLinkHandler(queries)
	domainHandler := handlers.NewDomainHandler(queries)

	// Middleware
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Health route
	r.GET("/healthz", handlers.HealthCheck)

	if authServer != nil && authServer.Handler() != nil {
		r.Any("/auth", gin.WrapH(authServer.Handler()))
		r.Any("/auth/*path", gin.WrapH(authServer.Handler()))
	}

	// API v1 routes
	v1 := r.Group("/api/v1")
	{
		v1.POST("/users", handlers.CreateUser)

		links := v1.Group("/links", authServer.RequireSession())
		{
			links.POST("", linkHandler.Create)
			links.GET("", linkHandler.List)
			links.DELETE("/:id", linkHandler.Delete)
		}

		domains := v1.Group("/domains", authServer.RequireSession())
		{
			domains.POST("", domainHandler.Create)
			domains.GET("", domainHandler.List)
			domains.DELETE("/:id", domainHandler.Delete)
		}
	}

	r.NoRoute(linkHandler.Redirect)

	return r
}
