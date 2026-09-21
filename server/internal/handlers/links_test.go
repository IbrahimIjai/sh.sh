package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const testBaseURL = "http://localhost:8080"

// fakeQueries keeps links in memory. Methods the tests don't use fall through to the embedded
// nil Querier and panic, which flags any unexpected query.
type fakeQueries struct {
	db.Querier
	mu    sync.Mutex
	links []db.Link
	now   time.Time

	breakdown []db.ListClickBreakdownByLinkRow
	daily     []db.ListDailyClicksByLinkRow
	totals    db.GetClickTotalsByLinkRow
	statsArgs []time.Time
}

func (f *fakeQueries) CreateLink(_ context.Context, arg db.CreateLinkParams) (db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links {
		if l.Slug == arg.Slug {
			return db.Link{}, errors.New("unexpected duplicate slug in test")
		}
	}
	link := db.Link{
		ID:             int64(len(f.links) + 1),
		UserID:         arg.UserID,
		Slug:           arg.Slug,
		TargetUrl:      arg.TargetUrl,
		Title:          arg.Title,
		Description:    arg.Description,
		IsActive:       true,
		CreatedAt:      f.now,
		UpdatedAt:      f.now,
		ClaimTokenHash: arg.ClaimTokenHash,
		CreatorHash:    arg.CreatorHash,
	}
	f.links = append(f.links, link)
	return link, nil
}

func (f *fakeQueries) SlugExists(_ context.Context, slug string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.ContainsFunc(f.links, func(l db.Link) bool { return l.Slug == slug }), nil
}

func (f *fakeQueries) CountLinksByCreatorSince(_ context.Context, arg db.CountLinksByCreatorSinceParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, l := range f.links {
		if l.CreatorHash == arg.CreatorHash && !l.CreatedAt.Before(arg.CreatedAt) {
			count++
		}
	}
	return count, nil
}

func (f *fakeQueries) GetLinkBySlug(_ context.Context, slug string) (db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links {
		if l.Slug == slug && l.IsActive && !l.DeletedAt.Valid {
			return l, nil
		}
	}
	return db.Link{}, sql.ErrNoRows
}

func (f *fakeQueries) GetLinkByID(_ context.Context, id int64) (db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links {
		if l.ID == id && !l.DeletedAt.Valid {
			return l, nil
		}
	}
	return db.Link{}, sql.ErrNoRows
}

func (f *fakeQueries) ListLinksByClaimTokens(_ context.Context, hashes []string) ([]db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []db.Link
	for _, l := range f.links {
		if l.ClaimTokenHash.Valid && slices.Contains(hashes, l.ClaimTokenHash.String) {
			found = append(found, l)
		}
	}
	return found, nil
}

func (f *fakeQueries) ClaimLinks(_ context.Context, arg db.ClaimLinksParams) ([]db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var claimed []db.Link
	for i, l := range f.links {
		if !l.UserID.Valid && l.ClaimTokenHash.Valid && slices.Contains(arg.ClaimTokenHashes, l.ClaimTokenHash.String) {
			f.links[i].UserID = arg.UserID
			f.links[i].ClaimTokenHash = sql.NullString{}
			claimed = append(claimed, f.links[i])
		}
	}
	return claimed, nil
}

func (f *fakeQueries) GetClickTotalsByLink(_ context.Context, arg db.GetClickTotalsByLinkParams) (db.GetClickTotalsByLinkRow, error) {
	f.statsArgs = append(f.statsArgs, arg.CreatedAt)
	return f.totals, nil
}

func (f *fakeQueries) ListDailyClicksByLink(_ context.Context, _ db.ListDailyClicksByLinkParams) ([]db.ListDailyClicksByLinkRow, error) {
	return f.daily, nil
}

func (f *fakeQueries) ListClickBreakdownByLink(_ context.Context, _ db.ListClickBreakdownByLinkParams) ([]db.ListClickBreakdownByLinkRow, error) {
	return f.breakdown, nil
}

type fakeClicks struct {
	mu     sync.Mutex
	clicks []analytics.Click
}

func (f *fakeClicks) Record(click analytics.Click) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clicks = append(f.clicks, click)
}

type fakeChecker struct {
	threats []string
	err     error
}

func (f fakeChecker) Check(context.Context, string) ([]string, error) {
	return f.threats, f.err
}

type testServer struct {
	queries *fakeQueries
	clicks  *fakeClicks
	handler *LinkHandler
}

func newTestServer(checker URLChecker) *testServer {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	queries := &fakeQueries{now: now}
	clicks := &fakeClicks{}
	hasher := analytics.NewHasher("test-secret")
	handler := NewLinkHandler(queries, LinkConfig{
		BaseURL:   testBaseURL + "/",
		Hasher:    hasher,
		Collector: analytics.NewCollector(analytics.CollectorConfig{Hasher: hasher, OwnHost: "localhost:8080"}),
		Clicks:    clicks,
		Safety:    checker,
	})
	handler.now = func() time.Time { return now }
	return &testServer{queries: queries, clicks: clicks, handler: handler}
}

// do sends a request as userID (0 = anonymous) through the same routes the real router uses.
func (s *testServer) do(t *testing.T, userID int64, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	asUser := func(c *gin.Context) {
		if userID != 0 {
			c.Set(auth.UserIDKey, userID)
		}
	}
	r.POST("/api/v1/links", asUser, s.handler.Create)
	r.POST("/api/v1/links/lookup", s.handler.Lookup)
	r.POST("/api/v1/links/claim", asUser, s.handler.Claim)
	r.GET("/api/v1/links/:id/stats", asUser, s.handler.Stats)
	r.NoRoute(s.handler.Redirect)

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return v
}

func TestCreateAnonymousLinkReturnsClaimToken(t *testing.T) {
	s := newTestServer(nil)

	w := s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com/page"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	resp := decode[linkResponse](t, w)
	if resp.ClaimToken == "" {
		t.Fatal("anonymous create should return a claim token")
	}
	if resp.ShortURL != testBaseURL+"/"+resp.Slug || len(resp.Slug) != 7 {
		t.Errorf("short_url = %q for slug %q", resp.ShortURL, resp.Slug)
	}

	stored := s.queries.links[0]
	if stored.UserID.Valid {
		t.Error("anonymous link should have no owner")
	}
	if stored.ClaimTokenHash.String != hashClaimToken(resp.ClaimToken) || stored.ClaimTokenHash.String == resp.ClaimToken {
		t.Error("only the claim token's hash should be stored")
	}
	if !stored.CreatorHash.Valid {
		t.Error("creator hash should be stored for rate limiting")
	}
	if strings.Contains(w.Body.String(), "claim_token_hash") || strings.Contains(w.Body.String(), "creator_hash") {
		t.Errorf("response leaks internal columns: %s", w.Body)
	}
}

func TestCreateAnonymousLimits(t *testing.T) {
	s := newTestServer(nil)

	w := s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com", "slug": "mine"}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("custom slug while anonymous: status = %d, want 401", w.Code)
	}

	for i := range anonymousLinksPerHour {
		if w := s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil); w.Code != http.StatusCreated {
			t.Fatalf("link %d: status = %d, body = %s", i, w.Code, w.Body)
		}
	}
	w = s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 after %d links", w.Code, anonymousLinksPerHour)
	}

	// Signed-in users aren't limited by the anonymous quota.
	if w := s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil); w.Code != http.StatusCreated {
		t.Errorf("signed-in create: status = %d, body = %s", w.Code, w.Body)
	}
}

func TestCreateValidatesTarget(t *testing.T) {
	s := newTestServer(nil)

	for _, target := range []string{
		"not-a-url",
		"ftp://example.com/file",
		"https://bit.ly/abc",
		"https://www.tinyurl.com/abc",
		testBaseURL + "/abc",
		"https://example.com/" + strings.Repeat("a", maxTargetURLLength),
	} {
		if w := s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": target}, nil); w.Code != http.StatusBadRequest {
			t.Errorf("target %.60q: status = %d, want 400", target, w.Code)
		}
	}
}

func TestCreateChecksWebRisk(t *testing.T) {
	flagged := newTestServer(fakeChecker{threats: []string{"SOCIAL_ENGINEERING"}})
	w := flagged.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://phish.example"}, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unsafe") {
		t.Errorf("flagged URL: status = %d, body = %s", w.Code, w.Body)
	}

	// A Web Risk outage must not block link creation.
	down := newTestServer(fakeChecker{err: errors.New("timeout")})
	if w := down.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil); w.Code != http.StatusCreated {
		t.Errorf("checker error: status = %d, want 201", w.Code)
	}
}

func TestCreateSignedInCustomSlug(t *testing.T) {
	s := newTestServer(nil)

	w := s.do(t, 5, "POST", "/api/v1/links", map[string]string{"target_url": "https://go.dev", "slug": "go-docs"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if resp := decode[linkResponse](t, w); resp.Slug != "go-docs" || resp.ClaimToken != "" {
		t.Errorf("slug = %q, claim token = %q; want go-docs and no token", resp.Slug, resp.ClaimToken)
	}
	if owner := s.queries.links[0].UserID; !owner.Valid || owner.Int64 != 5 {
		t.Errorf("owner = %+v, want user 5", owner)
	}

	for _, slug := range []string{"api", "Health", "a/b", "ab", "with+plus", strings.Repeat("x", 65)} {
		if w := s.do(t, 5, "POST", "/api/v1/links", map[string]string{"target_url": "https://go.dev", "slug": slug}, nil); w.Code != http.StatusBadRequest {
			t.Errorf("slug %q: status = %d, want 400", slug, w.Code)
		}
	}
}

func TestRedirectRecordsClickAndForwardsUTM(t *testing.T) {
	s := newTestServer(nil)
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

func TestPreviewPage(t *testing.T) {
	s := newTestServer(nil)
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

func TestLookupAndClaim(t *testing.T) {
	s := newTestServer(nil)
	created := decode[linkResponse](t, s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	tokens := map[string][]string{"claim_tokens": {created.ClaimToken, "unknown-token"}}

	found := decode[[]linkResponse](t, s.do(t, 0, "POST", "/api/v1/links/lookup", tokens, nil))
	if len(found) != 1 || found[0].ID != created.ID || found[0].ClaimToken != "" {
		t.Fatalf("lookup = %+v, want the created link without its token", found)
	}

	if w := s.do(t, 0, "POST", "/api/v1/links/claim", tokens, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous claim: status = %d, want 401", w.Code)
	}

	claimed := decode[struct {
		Claimed []linkResponse `json:"claimed"`
	}](t, s.do(t, 9, "POST", "/api/v1/links/claim", tokens, nil))
	if len(claimed.Claimed) != 1 || claimed.Claimed[0].ID != created.ID {
		t.Fatalf("claimed = %+v", claimed)
	}
	if owner := s.queries.links[0].UserID; owner.Int64 != 9 {
		t.Errorf("owner = %+v, want user 9", owner)
	}

	// The token is spent once claimed.
	if again := decode[[]linkResponse](t, s.do(t, 0, "POST", "/api/v1/links/lookup", tokens, nil)); len(again) != 0 {
		t.Errorf("lookup after claim = %+v, want none", again)
	}

	if w := s.do(t, 0, "POST", "/api/v1/links/lookup", map[string][]string{"claim_tokens": {}}, nil); w.Code != http.StatusBadRequest {
		t.Errorf("empty token list: status = %d, want 400", w.Code)
	}
}

func TestStats(t *testing.T) {
	s := newTestServer(nil)
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	path := "/api/v1/links/" + strconv.FormatInt(created.ID, 10) + "/stats?days=7"

	if w := s.do(t, 2, "GET", path, nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("someone else's link: status = %d, want 404", w.Code)
	}
	if w := s.do(t, 1, "GET", "/api/v1/links/1/stats?days=0", nil, nil); w.Code != http.StatusBadRequest {
		t.Errorf("days=0: status = %d, want 400", w.Code)
	}

	s.queries.totals = db.GetClickTotalsByLinkRow{Clicks: 5, UniqueVisitors: 3, BotClicks: 2}
	s.queries.daily = []db.ListDailyClicksByLinkRow{{Day: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), Clicks: 5, UniqueVisitors: 3}}
	s.queries.breakdown = []db.ListClickBreakdownByLinkRow{
		{Dimension: "sources", Name: "X (Twitter)", Clicks: 3},
		{Dimension: "sources", Name: "", Clicks: 2},
		{Dimension: "countries", Name: "", Clicks: 5},
		{Dimension: "utm_sources", Name: "", Clicks: 5},
	}

	w := s.do(t, 1, "GET", path, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	stats := decode[linkStatsResponse](t, w)

	if stats.Since != "2026-09-15" || s.queries.statsArgs[len(s.queries.statsArgs)-1] != time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("since = %q, want 2026-09-15 (7 days including today)", stats.Since)
	}
	if stats.Clicks != 5 || stats.UniqueVisitors != 3 || stats.BotClicks != 2 {
		t.Errorf("totals = %+v", stats)
	}
	if len(stats.Daily) != 7 || stats.Daily[5].Day != "2026-09-20" || stats.Daily[5].Clicks != 5 || stats.Daily[0].Clicks != 0 {
		t.Errorf("daily = %+v, want 7 zero-filled days with 5 clicks on 2026-09-20", stats.Daily)
	}
	if got := stats.Breakdown["sources"]; len(got) != 2 || got[1].Name != "Direct" {
		t.Errorf("sources = %+v, want X then Direct", got)
	}
	if got := stats.Breakdown["countries"]; len(got) != 1 || got[0].Name != "Unknown" {
		t.Errorf("countries = %+v, want Unknown", got)
	}
	if got, ok := stats.Breakdown["utm_sources"]; !ok || len(got) != 0 {
		t.Errorf("utm_sources = %+v, want present and empty", got)
	}
	if _, ok := stats.Breakdown["operating_systems"]; !ok {
		t.Error("every dimension should be present even with no rows")
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
