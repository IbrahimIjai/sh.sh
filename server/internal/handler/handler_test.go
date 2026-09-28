package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

const testBaseURL = "http://localhost:8080"

// fakeQueries keeps links in memory. Methods the tests don't use fall through to the embedded
// nil Querier and panic, which flags any unexpected query.
type fakeQueries struct {
	db.Querier
	mu    sync.Mutex
	links []db.Link
	now   time.Time

	slugLookups int

	breakdown []db.ListClickBreakdownByLinkRow
	daily     []db.ListDailyClicksByLinkRow
	totals    db.GetClickTotalsByLinkRow
	statsArgs []time.Time
}

// CreateLink mimics ON CONFLICT (slug) DO NOTHING: a taken slug returns no row.
func (f *fakeQueries) CreateLink(_ context.Context, arg db.CreateLinkParams) (db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links {
		if l.Slug == arg.Slug {
			return db.Link{}, sql.ErrNoRows
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
	f.slugLookups++
	for _, l := range f.links {
		if l.Slug == slug && l.IsActive && !l.DeletedAt.Valid {
			return l, nil
		}
	}
	return db.Link{}, sql.ErrNoRows
}

func (f *fakeQueries) SoftDeleteLink(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.links {
		if f.links[i].ID == id {
			f.links[i].DeletedAt = sql.NullTime{Time: f.now, Valid: true}
			f.links[i].IsActive = false
		}
	}
	return nil
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

type testOptions struct {
	checker   service.URLChecker
	cacheSize int
	newSlug   func() (string, error)
}

type testServer struct {
	queries   *fakeQueries
	clicks    *fakeClicks
	now       time.Time
	links     *LinkHandler
	redirects *RedirectHandler
}

// newTestServer wires the real services over fakeQueries. Tests move time forward by setting now.
func newTestServer(opts testOptions) *testServer {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s := &testServer{queries: &fakeQueries{now: now}, clicks: &fakeClicks{}, now: now}
	clock := func() time.Time { return s.now }
	hasher := analytics.NewHasher("test-secret")

	redirects := service.NewRedirectService(s.queries, service.RedirectConfig{
		CacheSize: opts.cacheSize,
		CacheTTL:  time.Minute,
		Now:       clock,
	})
	links := service.NewLinkService(s.queries, service.LinkConfig{
		Hasher:    hasher,
		Safety:    opts.checker,
		Redirects: redirects,
		OwnHost:   "localhost:8080",
		Now:       clock,
		NewSlug:   opts.newSlug,
	})
	collector := analytics.NewCollector(analytics.CollectorConfig{Hasher: hasher, OwnHost: "localhost:8080"})

	s.links = NewLinkHandler(links, testBaseURL+"/")
	s.redirects = NewRedirectHandler(redirects, collector, s.clicks, testBaseURL+"/")
	return s
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
	r.POST("/api/v1/links", asUser, s.links.Create)
	r.POST("/api/v1/links/lookup", s.links.Lookup)
	r.POST("/api/v1/links/claim", asUser, s.links.Claim)
	r.GET("/api/v1/links/:id/stats", asUser, s.links.Stats)
	r.DELETE("/api/v1/links/:id", asUser, s.links.Delete)
	r.NoRoute(s.redirects.Redirect)

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
