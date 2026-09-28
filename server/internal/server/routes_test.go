package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/handler"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

const testAppURL = "https://app.shortened.cv"

func newTestRouter(t *testing.T, appURL string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{BaseURL: "https://shortened.cv", AppURL: appURL}
	redirects := service.NewRedirectService(nil, service.RedirectConfig{CacheTTL: time.Minute})
	links := service.NewLinkService(nil, service.LinkConfig{Redirects: redirects})
	r, err := NewRouter(cfg, nil, nil, Handlers{
		Links:     handler.NewLinkHandler(links, cfg.BaseURL),
		Redirects: handler.NewRedirectHandler(redirects, nil, nil, cfg.BaseURL),
		Domains:   handler.NewDomainHandler(service.NewDomainService(nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func serve(r *gin.Engine, method, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{"))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCORSPreflightFromApp(t *testing.T) {
	w := serve(newTestRouter(t, testAppURL), http.MethodOptions, "/api/v1/links", testAppURL)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":      testAppURL,
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Allow-Headers":     "Content-Type",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("Access-Control-Allow-Methods = %q, want POST allowed", w.Header().Get("Access-Control-Allow-Methods"))
	}
}

func TestCORSHeadersOnAPIResponse(t *testing.T) {
	w := serve(newTestRouter(t, testAppURL), http.MethodPost, "/api/v1/links", testAppURL)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for the malformed body", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != testAppURL {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q so the UI can read the error", got, testAppURL)
	}
}

func TestCORSIgnoresOtherOrigins(t *testing.T) {
	r := newTestRouter(t, testAppURL)
	for _, origin := range []string{"https://evil.example", "https://app.shortened.cv.evil.example", ""} {
		w := serve(r, http.MethodOptions, "/api/v1/links", origin)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q: Access-Control-Allow-Origin = %q, want none", origin, got)
		}
		if w.Code == http.StatusNoContent {
			t.Errorf("origin %q: preflight answered with 204", origin)
		}
	}
}

func TestCORSLeavesRedirectsAlone(t *testing.T) {
	w := serve(newTestRouter(t, testAppURL), http.MethodOptions, "/abc123", testAppURL)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q on a slug path, want none", got)
	}
	if got := w.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q on a slug path, want none", got)
	}
}

func TestRootRedirectsToApp(t *testing.T) {
	w := serve(newTestRouter(t, testAppURL), http.MethodGet, "/", "")

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", w.Code)
	}
	if got := w.Header().Get("Location"); got != testAppURL {
		t.Errorf("Location = %q, want %q", got, testAppURL)
	}
}

func TestRootRedirectKeepsQuery(t *testing.T) {
	w := serve(newTestRouter(t, testAppURL), http.MethodGet, "/?error=invalid+token", "")

	if got, want := w.Header().Get("Location"), testAppURL+"/?error=invalid+token"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestNoAppURLKeepsOldBehaviour(t *testing.T) {
	r := newTestRouter(t, "")

	if w := serve(r, http.MethodGet, "/", ""); w.Code != http.StatusNotFound {
		t.Errorf("GET / status = %d, want 404 without APP_URL", w.Code)
	}
	if w := serve(r, http.MethodOptions, "/api/v1/links", testAppURL); w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS headers sent without APP_URL")
	}
}
