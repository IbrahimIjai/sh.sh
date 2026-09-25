package auth

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/thecodearcher/limen"
	sqladapter "github.com/thecodearcher/limen/adapters/sql"
)

type Server struct {
	limen   *limen.Limen
	handler http.Handler
}

func New(cfg config.Config, db *sql.DB) (*Server, error) {
	if cfg.LimenSecret == "" {
		return nil, fmt.Errorf("auth enabled but LIMEN_SECRET is not set")
	}
	emailSender := buildEmailSender(cfg)
	if emailSender == nil && cfg.Env == "production" {
		return nil, fmt.Errorf("magic-link email is not configured; production requires it")
	}

	limenAuth, err := limen.New(&limen.Config{
		BaseURL:  cfg.BaseURL,
		Database: sqladapter.NewPostgreSQL(db),
		Plugins:  buildPlugins(cfg, emailSender),
		Secret:   []byte(cfg.LimenSecret),
		CLI:      &limen.CLIConfig{Enabled: cfg.Env != "production"},
		HTTP:     limen.NewDefaultHTTPConfig(limen.WithHTTPCookieSecure(cfg.Env == "production")),
	})
	if err != nil {
		return nil, fmt.Errorf("create limen auth: %w", err)
	}

	return &Server{
		limen:   limenAuth,
		handler: limenAuth.Handler(),
	}, nil
}

func (s *Server) Handler() http.Handler {
	if s == nil {
		return nil
	}
	return s.handler
}
