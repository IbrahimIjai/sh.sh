package main

import (
	"context"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/database"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/handler"
	"github.com/ibrahimijai/sh.sh/internal/safety"
	"github.com/ibrahimijai/sh.sh/internal/server"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	conn, err := database.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer conn.Close()
	log.Printf("database pool: max open=%d, max idle=%d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)

	authServer, err := auth.New(cfg, conn)
	if err != nil {
		log.Fatalf("failed to configure auth: %v", err)
	}

	queries := db.New(conn)
	hasher := analytics.NewHasher(cfg.LimenSecret)

	var locator analytics.Locator
	if cfg.GeoIPDBPath != "" {
		geo, err := analytics.OpenGeoIP(cfg.GeoIPDBPath)
		if err != nil {
			log.Fatalf("failed to open geoip database: %v", err)
		}
		defer geo.Close()
		locator = geo
	} else if cfg.TrustedPlatform != "cloudflare" {
		log.Println("click geolocation disabled: set GEOIP_DB_PATH or TRUSTED_PLATFORM=cloudflare")
	}

	var urlChecker service.URLChecker
	if cfg.WebRiskAPIKey != "" {
		urlChecker = safety.NewWebRisk(cfg.WebRiskAPIKey)
	} else {
		log.Println("web risk check disabled: WEB_RISK_API_KEY is not set")
	}

	baseHost := ""
	if parsed, err := url.Parse(cfg.BaseURL); err == nil {
		baseHost = parsed.Host
	}

	clicks := analytics.NewRecorder(queries, analytics.RecorderConfig{})
	redirects := service.NewRedirectService(queries, service.RedirectConfig{
		CacheSize: cfg.LinkCacheSize,
		CacheTTL:  cfg.LinkCacheTTL,
	})
	log.Printf("link cache: size=%d ttl=%s", cfg.LinkCacheSize, cfg.LinkCacheTTL)
	links := service.NewLinkService(queries, service.LinkConfig{
		Hasher:    hasher,
		Safety:    urlChecker,
		Redirects: redirects,
		OwnHost:   baseHost,
	})
	collector := analytics.NewCollector(analytics.CollectorConfig{
		Hasher:     hasher,
		Locator:    locator,
		Cloudflare: cfg.TrustedPlatform == "cloudflare",
		OwnHost:    baseHost,
	})

	if cfg.Env == "production" && len(cfg.TrustedProxies) == 0 && cfg.TrustedPlatform == "" {
		log.Println("warning: TRUSTED_PROXIES and TRUSTED_PLATFORM are empty, so client IPs come from the TCP connection; " +
			"behind a load balancer every visitor shares its IP, which breaks the anonymous rate limit and unique-visitor counts")
	}

	router, err := server.NewRouter(cfg, conn, authServer, server.Handlers{
		Links:     handler.NewLinkHandler(links, cfg.BaseURL),
		Redirects: handler.NewRedirectHandler(redirects, collector, clicks, cfg.BaseURL),
		Domains:   handler.NewDomainHandler(service.NewDomainService(queries)),
	})
	if err != nil {
		log.Fatalf("failed to set up router: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Requests stop first, then the click recorder writes out what they buffered.
	if err := server.Run(ctx, ":"+cfg.Port, router, clicks.Close); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
