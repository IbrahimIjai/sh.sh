package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/middleware"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

func scrape(m *service.Metrics) string {
	w := httptest.NewRecorder()
	m.Exporter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return w.Body.String()
}

func TestRequestMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := service.NewMetrics()
	r := gin.New()
	r.Use(middleware.RequestMetrics(m), gin.RecoveryWithWriter(io.Discard))
	r.GET("/api/v1/links/:id/stats", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/private", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	r.GET("/panic", func(c *gin.Context) { panic("test") })
	r.NoRoute(func(c *gin.Context) {
		if c.Request.URL.Path == "/first-slug" || c.Request.URL.Path == "/second-slug" {
			c.Status(http.StatusFound)
			return
		}
		c.Status(http.StatusNotFound)
	})

	for _, request := range []struct{ method, path string }{
		{"GET", "/api/v1/links/123/stats?token=secret"},
		{"GET", "/api/v1/links/456/stats"},
		{"GET", "/first-slug"}, {"GET", "/second-slug"},
		{"GET", "/missing-slug"}, {"GET", "/missing-slug+"},
		{"GET", "/unknown/path"}, {"GET", "/private"}, {"GET", "/panic"},
		{"CUSTOM", "/anything"}, {"ANOTHER", "/anything-else"},
	} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(request.method, request.path, nil))
	}

	metrics := scrape(m)
	for _, want := range []string{
		`http_requests_total{method="GET",route="/api/v1/links/:id/stats",status="200"} 2`,
		`http_requests_total{method="GET",route="redirect",status="302"} 2`,
		`http_requests_total{method="GET",route="redirect",status="404"} 1`,
		`http_requests_total{method="GET",route="preview",status="404"} 1`,
		`http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`http_requests_total{method="GET",route="/private",status="401"} 1`,
		`http_requests_total{method="GET",route="/panic",status="500"} 1`,
		`http_requests_total{method="OTHER",route="unmatched",status="404"} 2`,
		`http_request_duration_seconds_count{method="GET",route="/api/v1/links/:id/stats"} 2`,
		`http_request_duration_seconds_count{method="GET",route="/panic"} 1`,
		`http_requests_in_flight 0`,
	} {
		if !strings.Contains(metrics, want+"\n") {
			t.Errorf("missing metric %s", want)
		}
	}
	for _, secret := range []string{"first-slug", "second-slug", "missing-slug", "123", "token=secret", "CUSTOM", "ANOTHER"} {
		if strings.Contains(metrics, `route="`+secret) || strings.Contains(metrics, `method="`+secret) {
			t.Errorf("request-specific label leaked: %s", secret)
		}
	}
	if got := strings.Count(metrics, "\nhttp_requests_total{"); got != 8 {
		t.Errorf("request series = %d, want 8 bounded label sets", got)
	}
}

func TestRequestMetricsInFlight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := service.NewMetrics()
	r := gin.New()
	r.Use(middleware.RequestMetrics(m))
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.GET("/slow", func(c *gin.Context) {
		close(entered)
		<-release
		c.Status(http.StatusOK)
	})
	go func() {
		defer close(done)
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))
	}()
	<-entered
	active := scrape(m)
	close(release)
	<-done
	if !strings.Contains(active, "\nhttp_requests_in_flight 1\n") {
		t.Error("active request was not counted")
	}
	if !strings.Contains(scrape(m), "\nhttp_requests_in_flight 0\n") {
		t.Error("completed request is still counted as in flight")
	}
}
