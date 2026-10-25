package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

// RequestMetrics wraps recovery and the other middleware so aborted requests and recovered
// panics are counted with their final status. Register it before gin.Recovery().
func RequestMetrics(m *service.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		m.RequestStarted()
		defer func() {
			m.RequestFinished(metricRoute(c), metricMethod(c.Request.Method), c.Writer.Status(), time.Since(start))
		}()
		c.Next()
	}
}

func metricRoute(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route // Gin's template, e.g. /api/v1/links/:id/stats, never an actual ID.
	}
	// Mirror the fallback handler's GET /{slug} and /{slug}+ shapes without labeling slugs.
	path, preview := strings.CutSuffix(strings.Trim(c.Request.URL.Path, "/"), "+")
	if c.Request.Method == http.MethodGet && path != "" && !strings.Contains(path, "/") {
		if preview {
			return "preview"
		}
		return "redirect"
	}
	return "unmatched"
}

func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}
