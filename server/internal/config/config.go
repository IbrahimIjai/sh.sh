package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port string
	Env  string

	BaseURL           string
	DatabaseURL       string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
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
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		DBMaxOpenConns:        maxOpen,
		DBMaxIdleConns:        maxIdle,
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
	}, nil
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

func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
