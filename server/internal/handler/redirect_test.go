package handler

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRedirectRecordsClickAndForwardsUTM(t *testing.T) {
	s := newTestServer(testOptions{})
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com/page?ref=1#top"}, nil))

	w := s.do(t, 0, "GET", "/"+created.Slug+"?utm_source=newsletter&utm_campaign=launch", nil, map[string]string{
		"User-Agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
		"Referer":    "https://t.co/abc",
	})

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got, want := w.Header().Get("Location"), "https://example.com/page?ref=1&utm_campaign=launch&utm_source=newsletter#top"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	if len(s.clicks.clicks) != 1 {
		t.Fatalf("recorded %d clicks, want 1", len(s.clicks.clicks))
	}
	click := s.clicks.clicks[0]
	if click.LinkID != created.ID || click.Source != "X (Twitter)" || click.UTMSource != "newsletter" || click.DeviceType != "mobile" {
		t.Errorf("unexpected click: %+v", click)
	}

	if w := s.do(t, 0, "GET", "/missing", nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown slug: status = %d, want 404", w.Code)
	}
}

func TestRedirectCache(t *testing.T) {
	s := newTestServer(testOptions{cacheSize: 100})

	if w := s.do(t, 0, "GET", "/custom", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("before create: status = %d, want 404", w.Code)
	}
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com", "slug": "custom"}, nil))

	lookups := s.queries.slugLookups
	for range 3 {
		if w := s.do(t, 0, "GET", "/custom", nil, nil); w.Code != http.StatusFound {
			t.Fatalf("after create: status = %d, want 302", w.Code)
		}
	}
	if got := s.queries.slugLookups - lookups; got != 1 {
		t.Errorf("3 redirects made %d slug lookups, want 1", got)
	}

	if w := s.do(t, 1, "DELETE", "/api/v1/links/"+strconv.FormatInt(created.ID, 10), nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", w.Code)
	}
	if w := s.do(t, 0, "GET", "/custom", nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d, want 404", w.Code)
	}
}

func TestRedirectCacheRespectsExpiry(t *testing.T) {
	s := newTestServer(testOptions{cacheSize: 100})
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	s.queries.links[0].ExpiresAt = sql.NullTime{Time: s.queries.now.Add(time.Minute), Valid: true}

	if w := s.do(t, 0, "GET", "/"+created.Slug, nil, nil); w.Code != http.StatusFound {
		t.Fatalf("before expiry: status = %d, want 302", w.Code)
	}
	lookups := s.queries.slugLookups
	s.now = s.queries.now.Add(2 * time.Minute)
	if w := s.do(t, 0, "GET", "/"+created.Slug, nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("after expiry: status = %d, want 404", w.Code)
	}
	if s.queries.slugLookups != lookups {
		t.Error("expected the expired link to be rejected from the cache")
	}
}

func TestPreviewPage(t *testing.T) {
	s := newTestServer(testOptions{})
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{
		"target_url": "https://example.com/a?b=<script>",
		"title":      "Example <b>title</b>",
	}, nil))

	w := s.do(t, 0, "GET", "/"+created.Slug+"+?utm_source=x", nil, nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d, content-type = %q", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	for _, want := range []string{"example.com", `href="/` + created.Slug + `?utm_source=x"`, "Example &lt;b&gt;title&lt;/b&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>title") {
		t.Error("preview must escape link content")
	}
	if len(s.clicks.clicks) != 0 {
		t.Error("viewing the preview must not count as a click")
	}

	if w := s.do(t, 0, "GET", "/missing+", nil, nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "doesn't exist") {
		t.Errorf("unknown preview: status = %d", w.Code)
	}
}

func TestWithForwardedParams(t *testing.T) {
	tests := []struct {
		target, query, want string
	}{
		{"https://example.com", "", "https://example.com"},
		{"https://example.com/a?x=%2F", "foo=bar", "https://example.com/a?x=%2F"},
		{"https://example.com/a", "utm_source=news", "https://example.com/a?utm_source=news"},
		{"https://example.com/a?utm_source=own", "utm_source=news&utm_medium=email", "https://example.com/a?utm_source=own&utm_medium=email"},
		{"https://example.com/a#frag", "utm_campaign=x", "https://example.com/a?utm_campaign=x#frag"},
	}
	for _, tt := range tests {
		query, _ := url.ParseQuery(tt.query)
		if got := withForwardedParams(tt.target, query); got != tt.want {
			t.Errorf("withForwardedParams(%q, %q) = %q, want %q", tt.target, tt.query, got, tt.want)
		}
	}
}
