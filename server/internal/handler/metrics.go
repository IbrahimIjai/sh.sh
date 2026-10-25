package handler

import (
	"net/http"
	"net/http/pprof"

	"github.com/ibrahimijai/sh.sh/internal/service"
)

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
