package main

import (
	"context"
	"errors"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/clickstream"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/database"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/handler"
	"github.com/ibrahimijai/sh.sh/internal/rediscache"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := database.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer conn.Close()
	log.Printf("database pool: max open=%d, max idle=%d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)

	if cfg.RunMigrations {
		applied, version, err := database.Migrate(ctx, conn)
		for _, file := range applied {
			log.Printf("migrations: applied %s", file)
		}
		if err != nil {
			log.Fatalf("failed to run migrations: %v", err)
		}
		log.Printf("migrations: schema at version %d", version)
	} else {
		log.Println("migrations: skipped (RUN_MIGRATIONS=false)")
	}

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

	// Interfaces stay nil without Redis; a nil *rediscache.Store inside them would not be.
	var sharedCache service.SharedCache
	var limiter service.RateLimiter
	if cfg.RedisURL != "" {
		store, err := rediscache.Open(cfg.RedisURL)
		if store == nil {
			log.Fatalf("invalid redis configuration: %v", err)
		}
		if err != nil {
			log.Printf("redis unreachable at startup, falling back until it recovers: %v", err)
		}
		defer store.Close()
		sharedCache, limiter = store, store
		log.Println("redis: shared redirect cache, invalidation and rate limit enabled")
	} else {
		log.Println("redis disabled: REDIS_URL is not set")
	}

	baseHost := ""
	if parsed, err := url.Parse(cfg.BaseURL); err == nil {
		baseHost = parsed.Host
	}

	clickStore := analytics.NewPostgresStore(conn, cfg.ClickAsyncCommit)
	recorder := analytics.NewRecorder(clickStore, analytics.RecorderConfig{
		BufferSize: cfg.ClickBufferSize,
		BatchSize:  cfg.ClickBatchSize,
		Writers:    cfg.ClickWriters,
	})
	var clicks handler.ClickRecorder = recorder
	closeClicks := recorder.Close
	var stream *clickstream.Queue
	if cfg.ClickQueue == "redis" {
		// The recorder stays as the stream's fallback while Redis is down or the stream is over budget.
		// Entries leave the stream once their write commits, so that commit must be durable.
		stream, err = clickstream.New(cfg.RedisURL, analytics.NewPostgresStore(conn, false), recorder, clickstream.Config{
			Buffer:    cfg.ClickBufferSize,
			BatchSize: cfg.ClickBatchSize,
			Consumers: cfg.ClickWriters,
			MaxBytes:  int64(cfg.ClickStreamMaxMB) << 20,
		})
		if err != nil {
			log.Fatalf("invalid click stream configuration: %v", err)
		}
		clicks = stream
		closeClicks = func(ctx context.Context) error {
			return errors.Join(stream.Close(ctx), recorder.Close(ctx))
		}
	}
	log.Printf("clicks: queue=%s buffer=%d batch=%d writers=%d async_commit=%v stream_budget=%dMiB",
		cfg.ClickQueue, cfg.ClickBufferSize, cfg.ClickBatchSize, cfg.ClickWriters, cfg.ClickAsyncCommit, cfg.ClickStreamMaxMB)

	redirects := service.NewRedirectService(queries, service.RedirectConfig{
		CacheSize:  cfg.LinkCacheSize,
		CacheTTL:   cfg.LinkCacheTTL,
		StaleFor:   cfg.LinkCacheStale,
		MaxDBLoads: max(1, cfg.DBMaxOpenConns/2),
		Shared:     sharedCache,
	})
	redirects.Start(ctx)
	log.Printf("link cache: size=%d ttl=%s stale=%s", cfg.LinkCacheSize, cfg.LinkCacheTTL, cfg.LinkCacheStale)

	links := service.NewLinkService(queries, service.LinkConfig{
		Hasher:               hasher,
		Safety:               urlChecker,
		Redirects:            redirects,
		Limiter:              limiter,
		OwnHost:              baseHost,
		MaxConcurrentReports: max(1, cfg.DBMaxOpenConns/4),
	})
	collector := analytics.NewCollector(analytics.CollectorConfig{
		Hasher:     hasher,
		Locator:    locator,
		Cloudflare: cfg.TrustedPlatform == "cloudflare",
		OwnHost:    baseHost,
	})

	maintainer := service.NewMaintainer(conn, service.MaintenanceConfig{
		RawRetentionDays: cfg.ClickRawRetentionDays,
		Every:            cfg.MaintenanceInterval,
	})
	if err := maintainer.EnsurePartitions(ctx); err != nil {
		log.Printf("maintenance: %v", err)
	}
	maintainer.Start(ctx)

	if cfg.Env == "production" && len(cfg.TrustedProxies) == 0 && cfg.TrustedPlatform == "" {
		log.Println("warning: TRUSTED_PROXIES and TRUSTED_PLATFORM are empty, so client IPs come from the TCP connection; " +
			"behind a load balancer every visitor shares its IP, which breaks the anonymous rate limit and unique-visitor counts")
	}

	handlers := server.Handlers{
		Links:     handler.NewLinkHandler(links, cfg.BaseURL),
		Redirects: handler.NewRedirectHandler(redirects, collector, clicks, cfg.BaseURL),
		Domains:   handler.NewDomainHandler(service.NewDomainService(queries)),
		Admin:     handler.NewAdminHandler(links),
	}
	if cfg.AdminToken == "" {
		log.Println("admin API disabled: ADMIN_TOKEN is not set")
	}
	if cfg.MetricsAddr != "" {
		m := service.NewMetrics()
		m.Track(conn, redirects, recorder, stream)
		handlers.Metrics = handler.RequestMetrics(m)
		server.ServeInternal(ctx, cfg.MetricsAddr, handler.NewInternalHandler(m))
	}

	router, err := server.NewRouter(cfg, conn, authServer, handlers)
	if err != nil {
		log.Fatalf("failed to set up router: %v", err)
	}

	// Requests stop first, then the click queue writes out what they buffered.
	if err := server.Run(ctx, ":"+cfg.Port, router, closeClicks); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
