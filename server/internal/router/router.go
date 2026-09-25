package router

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/handlers"
)

func SetupRouter(cfg config.Config, db handlers.Pinger, authServer *auth.Server, linkHandler *handlers.LinkHandler, domainHandler *handlers.DomainHandler) (*gin.Engine, error) {
	r := gin.New()

	// ClientIP() feeds visitor hashes, geo lookups and the anonymous rate limit. Gin trusts
	// X-Forwarded-For from anyone by default, which would let clients fake all three.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("set trusted proxies: %w", err)
	}
	switch cfg.TrustedPlatform {
	case "":
	case "cloudflare":
		r.TrustedPlatform = gin.PlatformCloudflare
	default:
		return nil, fmt.Errorf("unsupported TRUSTED_PLATFORM %q (supported: cloudflare)", cfg.TrustedPlatform)
	}

	// Middleware
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.GET("/health", handlers.HealthCheck)
	r.GET("/health/ready", handlers.Ready(db))

	if authServer != nil && authServer.Handler() != nil {
		r.Any("/auth", gin.WrapH(authServer.Handler()))
		r.Any("/auth/*path", gin.WrapH(authServer.Handler()))
	}

	// API v1 routes
	v1 := r.Group("/api/v1")
	{
		// Anyone can shorten a link; anonymous links come back with a claim token.
		v1.POST("/links", authServer.OptionalSession(), linkHandler.Create)
		v1.POST("/links/lookup", linkHandler.Lookup)

		links := v1.Group("/links", authServer.RequireSession())
		{
			links.GET("", linkHandler.List)
			links.POST("/claim", linkHandler.Claim)
			links.GET("/:id/stats", linkHandler.Stats)
			links.DELETE("/:id", linkHandler.Delete)
		}

		domains := v1.Group("/domains", authServer.RequireSession())
		{
			domains.POST("", domainHandler.Create)
			domains.GET("", domainHandler.List)
			domains.DELETE("/:id", domainHandler.Delete)
		}
	}

	// GET /{slug} redirects, GET /{slug}+ previews.
	r.NoRoute(linkHandler.Redirect)

	return r, nil
}
