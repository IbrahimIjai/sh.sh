package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/middleware"
	"github.com/ibrahimijai/sh.sh/internal/service"
	"github.com/lib/pq"
)

const testBaseURL = "http://localhost:8080"

// fakeQueries keeps links in memory. Methods the tests don't use fall through to the embedded
// nil Querier and panic, which flags any unexpected query.
type fakeQueries struct {
	db.Querier
	mu    sync.Mutex
	links []db.Link
	slugs []db.LinkSlug
	now   time.Time

	slugLookups int
	counters    map[int64]int64

	breakdown []db.ListClickDimensionsByLinkRow
	daily     []db.ListDailyClicksByLinkRow
	totals    db.GetClickTotalsByLinkRow
	statsArgs []time.Time
	statsGate chan struct{}

	rolledDays    []time.Time
	rollupTotals  db.GetRollupTotalsByLinkRow
	rollupDaily   []db.ListRollupDailyByLinkRow
	rollupDims    []db.ListRollupDimensionsByLinkRow
	rollupWindows []db.GetRollupTotalsByLinkParams
}

// CreateLink mimics ON CONFLICT DO NOTHING on link_slugs: a slug any link has ever had, or a
// custom look-alike of one, returns no row.
func (f *fakeQueries) CreateLink(_ context.Context, arg db.CreateLinkParams) (db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.slugIndex(arg.Slug) >= 0 || (arg.Custom && f.lookAlike(arg.Slug)) {
		return db.Link{}, sql.ErrNoRows
	}
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
	f.slugs = append(f.slugs, db.LinkSlug{Slug: arg.Slug, LinkID: link.ID, Custom: arg.Custom, CreatedAt: f.now})
	return link, nil
}

func (f *fakeQueries) slugIndex(slug string) int {
	return slices.IndexFunc(f.slugs, func(s db.LinkSlug) bool { return s.Slug == slug })
}

func (f *fakeQueries) lookAlike(slug string) bool {
	return slices.ContainsFunc(f.slugs, func(s db.LinkSlug) bool { return s.Custom && strings.EqualFold(s.Slug, slug) })
}

func (f *fakeQueries) ownerOf(linkID int64) int64 {
	for _, l := range f.links {
		if l.ID == linkID {
			return l.UserID.Int64
		}
	}
	return 0
}

func (f *fakeQueries) ChangeLinkSlug(_ context.Context, arg db.ChangeLinkSlugParams) (db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.links, func(l db.Link) bool {
		return l.ID == arg.ID && l.UserID.Valid && l.UserID.Int64 == arg.UserID && !l.DeletedAt.Valid
	})
	if i < 0 {
		return db.Link{}, sql.ErrNoRows
	}
	switch j := f.slugIndex(arg.Slug); {
	case j >= 0 && f.slugs[j].LinkID != arg.ID:
		return db.Link{}, sql.ErrNoRows
	case j >= 0:
	case f.lookAlike(arg.Slug):
		return db.Link{}, &pq.Error{Code: "23505"}
	default:
		f.slugs = append(f.slugs, db.LinkSlug{Slug: arg.Slug, LinkID: arg.ID, Custom: true, CreatedAt: f.now})
	}
	if j := f.slugIndex(f.links[i].Slug); j >= 0 {
		f.slugs[j].ReplacedAt = sql.NullTime{Time: f.now, Valid: true}
	}
	f.links[i].Slug = arg.Slug
	f.links[i].UpdatedAt = f.now
	return f.links[i], nil
}

func (f *fakeQueries) CountSlugChangesSince(_ context.Context, arg db.CountSlugChangesSinceParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, s := range f.slugs {
		if s.ReplacedAt.Valid && !s.ReplacedAt.Time.Before(arg.Since) && f.ownerOf(s.LinkID) == arg.UserID {
			count++
		}
	}
	return count, nil
}

func (f *fakeQueries) ListLinkSlugs(_ context.Context, ids []int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var slugs []string
	for _, s := range f.slugs {
		if slices.Contains(ids, s.LinkID) {
			slugs = append(slugs, s.Slug)
		}
	}
	return slugs, nil
}

// ListPreviousSlugs orders by replacement time, then most recently added first, because every
// change in a test can share one timestamp.
func (f *fakeQueries) ListPreviousSlugs(_ context.Context, ids []int64) ([]db.ListPreviousSlugsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current := map[string]bool{}
	for _, l := range f.links {
		current[l.Slug] = true
	}
	var replaced []db.LinkSlug
	for i := len(f.slugs) - 1; i >= 0; i-- {
		if s := f.slugs[i]; s.ReplacedAt.Valid && !current[s.Slug] && slices.Contains(ids, s.LinkID) {
			replaced = append(replaced, s)
		}
	}
	slices.SortStableFunc(replaced, func(a, b db.LinkSlug) int { return b.ReplacedAt.Time.Compare(a.ReplacedAt.Time) })
	rows := make([]db.ListPreviousSlugsRow, len(replaced))
	for i, s := range replaced {
		rows[i] = db.ListPreviousSlugsRow{LinkID: s.LinkID, Slug: s.Slug}
	}
	return rows, nil
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
	var id int64
	if i := slices.IndexFunc(f.links, func(l db.Link) bool { return l.Slug == slug }); i >= 0 {
		id = f.links[i].ID
	} else if j := f.slugIndex(slug); j >= 0 {
		id = f.slugs[j].LinkID
	}
	for _, l := range f.links {
		if l.ID == id && l.IsActive && !l.DeletedAt.Valid {
			return l, nil
		}
	}
	return db.Link{}, sql.ErrNoRows
}

func (f *fakeQueries) ListLinksByUser(_ context.Context, arg db.ListLinksByUserParams) ([]db.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []db.Link
	for i := len(f.links) - 1; i >= 0; i-- {
		if l := f.links[i]; l.UserID == arg.UserID && !l.DeletedAt.Valid {
			found = append(found, l)
		}
	}
	return found, nil
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
	f.mu.Lock()
	f.statsArgs = append(f.statsArgs, arg.CreatedAt)
	gate := f.statsGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return f.totals, nil
}

func (f *fakeQueries) statsCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.statsArgs)
}

func (f *fakeQueries) ListDailyClicksByLink(_ context.Context, _ db.ListDailyClicksByLinkParams) ([]db.ListDailyClicksByLinkRow, error) {
	return f.daily, nil
}

func (f *fakeQueries) ListClickDimensionsByLink(_ context.Context, _ db.ListClickDimensionsByLinkParams) ([]db.ListClickDimensionsByLinkRow, error) {
	return f.breakdown, nil
}

func (f *fakeQueries) ListRolledDays(_ context.Context, arg db.ListRolledDaysParams) ([]time.Time, error) {
	var days []time.Time
	for _, d := range f.rolledDays {
		if !d.Before(arg.Since) && d.Before(arg.Until) {
			days = append(days, d)
		}
	}
	return days, nil
}

func (f *fakeQueries) GetRollupTotalsByLink(_ context.Context, arg db.GetRollupTotalsByLinkParams) (db.GetRollupTotalsByLinkRow, error) {
	f.rollupWindows = append(f.rollupWindows, arg)
	return f.rollupTotals, nil
}

func (f *fakeQueries) ListRollupDailyByLink(_ context.Context, _ db.ListRollupDailyByLinkParams) ([]db.ListRollupDailyByLinkRow, error) {
	return f.rollupDaily, nil
}

func (f *fakeQueries) ListRollupDimensionsByLink(_ context.Context, _ db.ListRollupDimensionsByLinkParams) ([]db.ListRollupDimensionsByLinkRow, error) {
	return f.rollupDims, nil
}

func (f *fakeQueries) ListClickCounts(_ context.Context, ids []int64) ([]db.LinkCounter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []db.LinkCounter
	for _, id := range ids {
		if n, ok := f.counters[id]; ok {
			rows = append(rows, db.LinkCounter{LinkID: id, HumanClicks: n})
		}
	}
	return rows, nil
}

func (f *fakeQueries) DeactivateLinksBySlugs(_ context.Context, slugs []string) ([]db.DeactivateLinksBySlugsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	named := map[int64]bool{}
	for _, s := range f.slugs {
		if slices.Contains(slugs, s.Slug) {
			named[s.LinkID] = true
		}
	}
	var taken []db.DeactivateLinksBySlugsRow
	for i, l := range f.links {
		if l.IsActive && (named[l.ID] || slices.Contains(slugs, l.Slug)) {
			f.links[i].IsActive = false
			taken = append(taken, db.DeactivateLinksBySlugsRow{ID: l.ID, Slug: l.Slug})
		}
	}
	return taken, nil
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
	checker    service.URLChecker
	cacheSize  int
	newSlug    func() (string, error)
	maxReports int
}

const testAdminToken = "test-admin-token-0123456789abcdef"

type testServer struct {
	queries   *fakeQueries
	clicks    *fakeClicks
	now       time.Time
	links     *LinkHandler
	redirects *RedirectHandler
	admin     *AdminHandler
}

// newTestServer wires the real services over fakeQueries. Tests move time forward by setting now.
func newTestServer(opts testOptions) *testServer {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s := &testServer{queries: &fakeQueries{now: now, counters: map[int64]int64{}}, clicks: &fakeClicks{}, now: now}
	clock := func() time.Time { return s.now }
	hasher := analytics.NewHasher("test-secret")

	redirects := service.NewRedirectService(s.queries, service.RedirectConfig{
		CacheSize: opts.cacheSize,
		CacheTTL:  time.Minute,
		Now:       clock,
	})
	links := service.NewLinkService(s.queries, service.LinkConfig{
		Hasher:               hasher,
		Safety:               opts.checker,
		Redirects:            redirects,
		OwnHost:              "localhost:8080",
		Now:                  clock,
		NewSlug:              opts.newSlug,
		MaxConcurrentReports: opts.maxReports,
	})
	collector := analytics.NewCollector(analytics.CollectorConfig{Hasher: hasher, OwnHost: "localhost:8080"})

	s.links = NewLinkHandler(links, testBaseURL+"/")
	s.redirects = NewRedirectHandler(redirects, collector, s.clicks, testBaseURL+"/")
	s.admin = NewAdminHandler(links)
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
	r.GET("/api/v1/links", asUser, s.links.List)
	r.POST("/api/v1/links/lookup", s.links.Lookup)
	r.POST("/api/v1/links/claim", asUser, s.links.Claim)
	r.GET("/api/v1/links/:id/stats", asUser, s.links.Stats)
	r.PATCH("/api/v1/links/:id", asUser, s.links.Update)
	r.DELETE("/api/v1/links/:id", asUser, s.links.Delete)
	r.POST("/api/v1/admin/takedown", middleware.AdminToken(testAdminToken), s.admin.Takedown)
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
