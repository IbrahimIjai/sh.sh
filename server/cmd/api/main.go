package main

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/database"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/router"
)

func main() {
	cfg := config.Load()

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	conn, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer conn.Close()

	authServer, err := auth.New(cfg, conn)
	if err != nil {
		log.Fatalf("failed to configure auth: %v", err)
	}

	r := router.SetupRouter(authServer, db.New(conn))

	addr := fmt.Sprintf(":%s", cfg.Port)

	log.Printf("server running on http://localhost%s", addr)

	if err := r.Run(addr); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
