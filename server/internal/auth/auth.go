package auth

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"

	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/thecodearcher/limen"
	sqladapter "github.com/thecodearcher/limen/adapters/sql"
)

type Server struct {
	handler http.Handler
	db      *sql.DB
}

func New(cfg config.Config) (*Server, error) {
	if cfg.DatabaseURL == "" {
		log.Println("auth disabled: DATABASE_URL is not set")
		return nil, nil
	}

	if cfg.LimenSecret == "" {
		return nil, fmt.Errorf("auth enabled but LIMEN_SECRET is not set")
	}

	db, err := openDatabase(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	limenAuth, err := limen.New(&limen.Config{
		BaseURL:  cfg.BaseURL,
		Database: sqladapter.NewPostgreSQL(db),
		Plugins:  buildPlugins(cfg),
		Secret:   []byte(cfg.LimenSecret),
		CLI:      &limen.CLIConfig{Enabled: cfg.Env != "production"},
		HTTP:     limen.NewDefaultHTTPConfig(limen.WithHTTPCookieSecure(cfg.Env == "production")),
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create limen auth: %w", err)
	}

	return &Server{
		handler: limenAuth.Handler(),
		db:      db,
	}, nil
}

func (s *Server) Handler() http.Handler {
	if s == nil {
		return nil
	}
	return s.handler
}

func (s *Server) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
