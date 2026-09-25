package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/database"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/handlers"
	"github.com/ibrahimijai/sh.sh/internal/router"
	"github.com/ibrahimijai/sh.sh/internal/safety"
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

	var urlChecker handlers.URLChecker
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
	linkHandler := handlers.NewLinkHandler(queries, handlers.LinkConfig{
		BaseURL: cfg.BaseURL,
		Hasher:  hasher,
		Collector: analytics.NewCollector(analytics.CollectorConfig{
			Hasher:     hasher,
			Locator:    locator,
			Cloudflare: cfg.TrustedPlatform == "cloudflare",
			OwnHost:    baseHost,
		}),
		Clicks: clicks,
		Safety: urlChecker,
	})

	if cfg.Env == "production" && len(cfg.TrustedProxies) == 0 && cfg.TrustedPlatform == "" {
		log.Println("warning: TRUSTED_PROXIES and TRUSTED_PLATFORM are empty, so client IPs come from the TCP connection; " +
			"behind a load balancer every visitor shares its IP, which breaks the anonymous rate limit and unique-visitor counts")
	}

	r, err := router.SetupRouter(cfg, conn, authServer, linkHandler, handlers.NewDomainHandler(queries))
	if err != nil {
		log.Fatalf("failed to set up router: %v", err)
	}

	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("server running on http://localhost%s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	// Stop taking requests first, then write out the clicks those requests buffered.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if err := clicks.Close(shutdownCtx); err != nil {
		log.Printf("click recorder shutdown: %v", err)
	}
}
