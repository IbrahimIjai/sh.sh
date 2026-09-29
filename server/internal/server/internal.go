package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
)

// ServeInternal serves h on addr until ctx ends. Keep addr off the public proxy: Traefik only
// routes the domain to the API port, so an internal port stays private.
func ServeInternal(ctx context.Context, addr string, h http.Handler) {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	go func() {
		log.Printf("metrics on http://localhost%s/metrics", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics server: %v", err)
		}
	}()
}
