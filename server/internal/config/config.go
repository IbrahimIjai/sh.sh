package config

import "os"

type Config struct {
	Port string
	Env  string

	BaseURL           string
	DatabaseURL       string
	LimenSecret       string
	GoogleID          string
	GoogleSecret      string
	GoogleRedirectURL string

	ResendAPIKey          string
	ResendFrom            string
	MagicLinkEmailSubject string
}

func Load() Config {
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
		magicLinkEmailSubject = "Sign in to SH"
	}

	return Config{
		Port:                  port,
		Env:                   env,
		BaseURL:               baseURL,
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		LimenSecret:           os.Getenv("LIMEN_SECRET"),
		GoogleID:              os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleSecret:          os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:     googleRedirectURL,
		ResendAPIKey:          os.Getenv("RESEND_API_KEY"),
		ResendFrom:            os.Getenv("RESEND_FROM"),
		MagicLinkEmailSubject: magicLinkEmailSubject,
	}
}
