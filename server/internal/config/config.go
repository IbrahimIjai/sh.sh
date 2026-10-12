package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port string
	Env  string

	BaseURL     string
	DatabaseURL string
	// RunMigrations applies pending db/migrations at startup, before anything else touches the database.
	RunMigrations     bool
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	LinkCacheSize     int
	LinkCacheTTL      time.Duration
	LimenSecret       string
	GoogleID          string
	GoogleSecret      string
	GoogleRedirectURL string

	EmailProvider         string
	ResendAPIKey          string
	ResendFrom            string
	TransendAPIKey        string
	TransendFrom          string
	MagicLinkEmailSubject string

	// TrustedProxies lists the proxy IPs/CIDRs allowed to set X-Forwarded-For. Empty means the
	// client IP is always the TCP peer, which is right when nothing sits in front of the API.
	TrustedProxies []string
	// TrustedPlatform is "cloudflare" when the API is only reachable through Cloudflare.
	TrustedPlatform string
	// GeoIPDBPath points at a MaxMind GeoLite2-City .mmdb file for country/city lookups.
	GeoIPDBPath string
	// WebRiskAPIKey enables Google Web Risk checks on new links.
	WebRiskAPIKey string
	// AppURL is the UI's origin, e.g. https://app.shortened.cv. It gets CORS access and "/" redirects to it.
	AppURL string

	// LinkCacheStale serves an expired cached link this long past its TTL while the database fails.
	LinkCacheStale time.Duration
	// RedisURL enables the shared redirect cache, cross-instance invalidation and the Redis rate limit.
	RedisURL string
	// AdminToken enables /api/v1/admin/* for requests with "Authorization: Bearer <token>".
	AdminToken string
	// MetricsAddr, e.g. ":9090", serves /metrics and /debug/pprof on a separate, internal listener.
	MetricsAddr string
	// RequestLog is "all", "errors" (status >= 400) or "off".
	RequestLog string

	// ClickQueue is "redis" (clicks go through a Redis stream) or "memory" (straight from RAM to
	// PostgreSQL). It defaults to redis when REDIS_URL is set.
	ClickQueue string
	// ClickStreamMaxMB caps the stream's memory; beyond it, clicks go straight to PostgreSQL.
	ClickStreamMaxMB int

	ClickBufferSize       int
	ClickBatchSize        int
	ClickWriters          int
	ClickAsyncCommit      bool
	ClickRawRetentionDays int
	MaintenanceInterval   time.Duration
}

func Load() (Config, error) {
	maxOpen, err := envInt("DB_MAX_OPEN_CONNS", 20)
	if err != nil {
		return Config{}, err
	}
	maxIdle, err := envInt("DB_MAX_IDLE_CONNS", min(20, maxOpen))
	if err != nil {
		return Config{}, err
	}
	if maxOpen < 1 || maxIdle < 0 || maxIdle > maxOpen {
		return Config{}, fmt.Errorf("DB_MAX_OPEN_CONNS must be positive; DB_MAX_IDLE_CONNS must be between 0 and DB_MAX_OPEN_CONNS")
	}
	cacheSize, err := envInt("LINK_CACHE_SIZE", 100_000)
	if err != nil {
		return Config{}, err
	}
	cacheTTL, err := envDuration("LINK_CACHE_TTL", time.Minute)
	if err != nil {
		return Config{}, err
	}
	if cacheSize < 0 || cacheTTL <= 0 {
		return Config{}, fmt.Errorf("LINK_CACHE_SIZE must be 0 (off) or more; LINK_CACHE_TTL must be positive")
	}
	cacheStale, err := envDuration("LINK_CACHE_STALE", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	clickBuffer, err := envInt("CLICK_BUFFER_SIZE", 100_000)
	if err != nil {
		return Config{}, err
	}
	clickBatch, err := envInt("CLICK_BATCH_SIZE", 1000)
	if err != nil {
		return Config{}, err
	}
	clickWriters, err := envInt("CLICK_WRITERS", 2)
	if err != nil {
		return Config{}, err
	}
	retentionDays, err := envInt("CLICK_RAW_RETENTION_DAYS", 30)
	if err != nil {
		return Config{}, err
	}
	maintenanceEvery, err := envDuration("MAINTENANCE_INTERVAL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	if cacheStale < 0 || clickBuffer < 1 || clickBatch < 1 || clickWriters < 1 || retentionDays < 1 || maintenanceEvery <= 0 {
		return Config{}, fmt.Errorf("LINK_CACHE_STALE must be 0 or more; CLICK_BUFFER_SIZE, CLICK_BATCH_SIZE, CLICK_WRITERS, " +
			"CLICK_RAW_RETENTION_DAYS and MAINTENANCE_INTERVAL must be positive")
	}
	runMigrations, err := envBool("RUN_MIGRATIONS", true)
	if err != nil {
		return Config{}, err
	}
	asyncCommit, err := envBool("CLICK_ASYNC_COMMIT", true)
	if err != nil {
		return Config{}, err
	}
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	clickQueue := strings.ToLower(strings.TrimSpace(os.Getenv("CLICK_QUEUE")))
	switch clickQueue {
	case "":
		clickQueue = "memory"
		if redisURL != "" {
			clickQueue = "redis"
		}
	case "memory":
	case "redis":
		if redisURL == "" {
			return Config{}, fmt.Errorf("CLICK_QUEUE=redis needs REDIS_URL")
		}
	default:
		return Config{}, fmt.Errorf("CLICK_QUEUE must be redis or memory")
	}
	streamMaxMB, err := envInt("CLICK_STREAM_MAX_MB", 64)
	if err != nil {
		return Config{}, err
	}
	if streamMaxMB < 1 {
		return Config{}, fmt.Errorf("CLICK_STREAM_MAX_MB must be positive")
	}
	requestLog := strings.ToLower(strings.TrimSpace(os.Getenv("REQUEST_LOG")))
	switch requestLog {
	case "":
		requestLog = "all"
	case "all", "errors", "off":
	default:
		return Config{}, fmt.Errorf("REQUEST_LOG must be all, errors or off")
	}
	adminToken := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
	if adminToken != "" && len(adminToken) < 32 {
		return Config{}, fmt.Errorf("ADMIN_TOKEN must be at least 32 characters (try: openssl rand -hex 32)")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:" + port
	}

	googleRedirectURL := os.Getenv("GOOGLE_REDIRECT_URL")
	if googleRedirectURL == "" {
		googleRedirectURL = baseURL + "/auth/oauth/google/callback"
	}

	magicLinkEmailSubject := os.Getenv("MAGIC_LINK_EMAIL_SUBJECT")
	if magicLinkEmailSubject == "" {
		magicLinkEmailSubject = "Sign in to shortened.cv"
	}

	emailProvider := strings.ToLower(strings.TrimSpace(os.Getenv("EMAIL_PROVIDER")))
	if emailProvider == "" {
		emailProvider = "resend"
	}

	return Config{
		Port:                  port,
		Env:                   env,
		BaseURL:               baseURL,
		AppURL:                strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RunMigrations:         runMigrations,
		DBMaxOpenConns:        maxOpen,
		DBMaxIdleConns:        maxIdle,
		LinkCacheSize:         cacheSize,
		LinkCacheTTL:          cacheTTL,
		LimenSecret:           os.Getenv("LIMEN_SECRET"),
		GoogleID:              os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleSecret:          os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:     googleRedirectURL,
		EmailProvider:         emailProvider,
		ResendAPIKey:          os.Getenv("RESEND_API_KEY"),
		ResendFrom:            os.Getenv("RESEND_FROM"),
		TransendAPIKey:        os.Getenv("TRANSEND_API_KEY"),
		TransendFrom:          os.Getenv("TRANSEND_FROM"),
		MagicLinkEmailSubject: magicLinkEmailSubject,
		TrustedProxies:        splitList(os.Getenv("TRUSTED_PROXIES")),
		TrustedPlatform:       strings.ToLower(strings.TrimSpace(os.Getenv("TRUSTED_PLATFORM"))),
		GeoIPDBPath:           os.Getenv("GEOIP_DB_PATH"),
		WebRiskAPIKey:         os.Getenv("WEB_RISK_API_KEY"),
		LinkCacheStale:        cacheStale,
		RedisURL:              redisURL,
		AdminToken:            adminToken,
		MetricsAddr:           strings.TrimSpace(os.Getenv("METRICS_ADDR")),
		RequestLog:            requestLog,
		ClickQueue:            clickQueue,
		ClickStreamMaxMB:      streamMaxMB,
		ClickBufferSize:       clickBuffer,
		ClickBatchSize:        clickBatch,
		ClickWriters:          clickWriters,
		ClickAsyncCommit:      asyncCommit,
		ClickRawRetentionDays: retentionDays,
		MaintenanceInterval:   maintenanceEvery,
	}, nil
}

func envBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return b, nil
}

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return n, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration such as 60s", name)
	}
	return d, nil
}

func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
