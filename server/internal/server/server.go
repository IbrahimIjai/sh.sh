package server

import (
	"context"
	"log"
	"net/http"
	"time"
)

const shutdownBudget = 15 * time.Second

// Run serves until ctx is cancelled, then stops taking requests and calls afterShutdown with
// what is left of the same 15-second budget. It returns early only if the listener fails.
func Run(ctx context.Context, addr string, h http.Handler, afterShutdown func(context.Context) error) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("server running on http://localhost%s", addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if afterShutdown != nil {
		if err := afterShutdown(shutdownCtx); err != nil {
			log.Printf("after shutdown: %v", err)
		}
	}
	return nil
}
