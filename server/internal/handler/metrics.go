package handler

import (
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

// RequestMetrics records every request. Unmatched paths, which are short-link slugs, share the
// "redirect" route label so slugs never become label values.
func RequestMetrics(m *service.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "redirect"
		}
		m.ObserveRequest(route, c.Request.Method, c.Writer.Status(), time.Since(start))
	}
}

// NewInternalHandler serves /metrics and /debug/pprof for the internal listener (METRICS_ADDR).
func NewInternalHandler(m *service.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Exporter())
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
