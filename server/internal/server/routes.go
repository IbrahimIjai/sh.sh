package server

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/handler"
	"github.com/ibrahimijai/sh.sh/internal/middleware"
)

type Handlers struct {
	Links     *handler.LinkHandler
	Redirects *handler.RedirectHandler
	Domains   *handler.DomainHandler
}

func NewRouter(cfg config.Config, pinger handler.Pinger, authServer *auth.Server, h Handlers) (*gin.Engine, error) {
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

	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(middleware.CORS(cfg.AppURL))

	r.GET("/health", handler.HealthCheck)
	r.GET("/health/ready", handler.Ready(pinger))

	if cfg.AppURL != "" {
		// Keep the query: limen sends failed magic links to BASE_URL/?error=...
		r.GET("/", func(c *gin.Context) {
			target := cfg.AppURL
			if query := c.Request.URL.RawQuery; query != "" {
				target += "/?" + query
			}
			c.Redirect(http.StatusMovedPermanently, target)
		})
	}

	if authServer != nil && authServer.Handler() != nil {
		r.Any("/auth", gin.WrapH(authServer.Handler()))
		r.Any("/auth/*path", gin.WrapH(authServer.Handler()))
	}

	v1 := r.Group("/api/v1")
	{
		// Anyone can shorten a link; anonymous links come back with a claim token.
		v1.POST("/links", authServer.OptionalSession(), h.Links.Create)
		v1.POST("/links/lookup", h.Links.Lookup)

		links := v1.Group("/links", authServer.RequireSession())
		{
			links.GET("", h.Links.List)
			links.POST("/claim", h.Links.Claim)
			links.GET("/:id/stats", h.Links.Stats)
			links.DELETE("/:id", h.Links.Delete)
		}

		domains := v1.Group("/domains", authServer.RequireSession())
		{
			domains.POST("", h.Domains.Create)
			domains.GET("", h.Domains.List)
			domains.DELETE("/:id", h.Domains.Delete)
		}
	}

	// GET /{slug} redirects, GET /{slug}+ previews.
	r.NoRoute(h.Redirects.Redirect)

	return r, nil
}
