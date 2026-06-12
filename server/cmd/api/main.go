package main

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/router"
)

func main() {
	cfg := config.Load()

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	authServer, err := auth.New(cfg)
	if err != nil {
		log.Fatalf("failed to configure auth: %v", err)
	}
	defer authServer.Close()

	r := router.SetupRouter(authServer.Handler())

	addr := fmt.Sprintf(":%s", cfg.Port)

	log.Printf("server running on http://localhost%s", addr)

	if err := r.Run(addr); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
