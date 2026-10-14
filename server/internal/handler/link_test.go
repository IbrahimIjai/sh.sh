package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const anonymousLinksPerHour = 20

func TestCreateAnonymousLinkReturnsClaimToken(t *testing.T) {
	s := newTestServer(testOptions{})

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
	sum := sha256.Sum256([]byte(resp.ClaimToken))
	if stored.ClaimTokenHash.String != hex.EncodeToString(sum[:]) {
		t.Error("only the claim token's SHA-256 should be stored")
	}
	if !stored.CreatorHash.Valid {
		t.Error("creator hash should be stored for rate limiting")
	}
	if strings.Contains(w.Body.String(), "claim_token_hash") || strings.Contains(w.Body.String(), "creator_hash") {
		t.Errorf("response leaks internal columns: %s", w.Body)
	}
}

func TestCreateAnonymousLimits(t *testing.T) {
	s := newTestServer(testOptions{})

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
	s := newTestServer(testOptions{})

	for _, target := range []string{
		"not-a-url",
		"ftp://example.com/file",
		"https://bit.ly/abc",
		"https://www.tinyurl.com/abc",
		testBaseURL + "/abc",
		"https://example.com/" + strings.Repeat("a", 2048),
	} {
		if w := s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": target}, nil); w.Code != http.StatusBadRequest {
			t.Errorf("target %.60q: status = %d, want 400", target, w.Code)
		}
	}
}

func TestCreateChecksWebRisk(t *testing.T) {
	flagged := newTestServer(testOptions{checker: fakeChecker{threats: []string{"SOCIAL_ENGINEERING"}}})
	w := flagged.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://phish.example"}, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unsafe") {
		t.Errorf("flagged URL: status = %d, body = %s", w.Code, w.Body)
	}

	// A Web Risk outage must not block link creation.
	down := newTestServer(testOptions{checker: fakeChecker{err: errors.New("timeout")}})
	if w := down.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil); w.Code != http.StatusCreated {
		t.Errorf("checker error: status = %d, want 201", w.Code)
	}
}

func TestCreateSignedInCustomSlug(t *testing.T) {
	s := newTestServer(testOptions{})

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
	if w := s.do(t, 6, "POST", "/api/v1/links", map[string]string{"target_url": "https://go.dev", "slug": "go-docs"}, nil); w.Code != http.StatusConflict {
		t.Errorf("taken slug: status = %d, want 409", w.Code)
	}

	for _, slug := range []string{"api", "Health", "a/b", "ab", "with+plus", strings.Repeat("x", 129)} {
		if w := s.do(t, 5, "POST", "/api/v1/links", map[string]string{"target_url": "https://go.dev", "slug": slug}, nil); w.Code != http.StatusBadRequest {
			t.Errorf("slug %q: status = %d, want 400", slug, w.Code)
		}
	}
	if w := s.do(t, 5, "POST", "/api/v1/links", map[string]string{"target_url": "https://go.dev", "slug": strings.Repeat("x", 128)}, nil); w.Code != http.StatusCreated {
		t.Errorf("128-character slug: status = %d, want 201", w.Code)
	}
	if w := s.do(t, 6, "POST", "/api/v1/links", map[string]string{"target_url": "https://go.dev", "slug": "Go-Docs"}, nil); w.Code != http.StatusConflict {
		t.Errorf("look-alike of a custom slug: status = %d, want 409", w.Code)
	}
}

func TestChangeSlug(t *testing.T) {
	s := newTestServer(testOptions{})
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com/contacts"}, nil))
	path := "/api/v1/links/" + strconv.FormatInt(created.ID, 10)
	change := func(userID int64, slug string) *httptest.ResponseRecorder {
		return s.do(t, userID, "PATCH", path, map[string]string{"slug": slug}, nil)
	}

	w := change(1, "eglobaltech_contacts")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	changed := decode[linkResponse](t, w)
	if changed.Slug != "eglobaltech_contacts" || changed.ShortURL != testBaseURL+"/eglobaltech_contacts" {
		t.Errorf("slug = %q, short_url = %q", changed.Slug, changed.ShortURL)
	}
	if !slices.Equal(changed.PreviousSlugs, []string{created.Slug}) {
		t.Errorf("previous_slugs = %v, want [%s]", changed.PreviousSlugs, created.Slug)
	}

	for _, slug := range []string{"eglobaltech_contacts", created.Slug} {
		w := s.do(t, 0, "GET", "/"+slug, nil, nil)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "https://example.com/contacts" {
			t.Errorf("GET /%s: status = %d, location = %q", slug, w.Code, w.Header().Get("Location"))
		}
	}
	for _, click := range s.clicks.clicks {
		if click.LinkID != created.ID {
			t.Errorf("click recorded for link %d, want %d: old and new slugs share one link's stats", click.LinkID, created.ID)
		}
	}

	listed := decode[[]linkResponse](t, s.do(t, 1, "GET", "/api/v1/links", nil, nil))
	if len(listed) != 1 || listed[0].Slug != "eglobaltech_contacts" || !slices.Equal(listed[0].PreviousSlugs, []string{created.Slug}) {
		t.Errorf("list = %+v", listed)
	}

	if w := change(2, "someone-else"); w.Code != http.StatusNotFound {
		t.Errorf("another user's link: status = %d, want 404", w.Code)
	}
	if w := change(0, "anonymous"); w.Code != http.StatusUnauthorized {
		t.Errorf("signed out: status = %d, want 401", w.Code)
	}
	anonymous := decode[linkResponse](t, s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	if w := s.do(t, 1, "PATCH", "/api/v1/links/"+strconv.FormatInt(anonymous.ID, 10), map[string]string{"slug": "not-mine"}, nil); w.Code != http.StatusNotFound {
		t.Errorf("unclaimed anonymous link: status = %d, want 404", w.Code)
	}

	for _, slug := range []string{"ab", "api", "a/b", "with+plus", strings.Repeat("x", 129)} {
		if w := change(1, slug); w.Code != http.StatusBadRequest {
			t.Errorf("slug %q: status = %d, want 400", slug, w.Code)
		}
	}
	if w := s.do(t, 1, "PATCH", path, map[string]string{}, nil); w.Code != http.StatusBadRequest {
		t.Errorf("missing slug: status = %d, want 400", w.Code)
	}

	s.do(t, 2, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.org", "slug": "taken-name"}, nil)
	for _, slug := range []string{"taken-name", "Taken-Name"} {
		if w := change(1, slug); w.Code != http.StatusConflict {
			t.Errorf("slug %q held by another link: status = %d, want 409", slug, w.Code)
		}
	}

	w = change(1, created.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("back to the old slug: status = %d, body = %s", w.Code, w.Body)
	}
	if back := decode[linkResponse](t, w); back.Slug != created.Slug || !slices.Equal(back.PreviousSlugs, []string{"eglobaltech_contacts"}) {
		t.Errorf("slug = %q, previous_slugs = %v", back.Slug, back.PreviousSlugs)
	}
	if w := s.do(t, 3, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.net", "slug": "eglobaltech_contacts"}, nil); w.Code != http.StatusConflict {
		t.Errorf("reusing a replaced slug: status = %d, want 409", w.Code)
	}
	if w := s.do(t, 0, "GET", "/eglobaltech_contacts", nil, nil); w.Code != http.StatusFound {
		t.Errorf("replaced slug after changing back: status = %d, want 302", w.Code)
	}
}

func TestChangeSlugDailyLimit(t *testing.T) {
	s := newTestServer(testOptions{})
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	path := "/api/v1/links/" + strconv.FormatInt(created.ID, 10)

	for i := range 10 {
		if w := s.do(t, 1, "PATCH", path, map[string]string{"slug": "name-" + strconv.Itoa(i)}, nil); w.Code != http.StatusOK {
			t.Fatalf("change %d: status = %d, body = %s", i, w.Code, w.Body)
		}
	}
	if w := s.do(t, 1, "PATCH", path, map[string]string{"slug": "name-9"}, nil); w.Code != http.StatusOK {
		t.Errorf("keeping the current slug: status = %d, want 200", w.Code)
	}
	if w := s.do(t, 1, "PATCH", path, map[string]string{"slug": "one-too-many"}, nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("11th change: status = %d, want 429", w.Code)
	}

	s.now = s.now.Add(25 * time.Hour)
	if w := s.do(t, 1, "PATCH", path, map[string]string{"slug": "next-day"}, nil); w.Code != http.StatusOK {
		t.Errorf("a day later: status = %d, want 200", w.Code)
	}
}

func TestCreateRetriesTakenRandomSlug(t *testing.T) {
	var next func() (string, error)
	s := newTestServer(testOptions{newSlug: func() (string, error) { return next() }})
	if w := s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com", "slug": "Taken12"}, nil); w.Code != http.StatusCreated {
		t.Fatalf("setup: status = %d", w.Code)
	}

	candidates := []string{"Taken12", "Free123"}
	next = func() (string, error) {
		slug := candidates[0]
		candidates = candidates[1:]
		return slug, nil
	}
	w := s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if resp := decode[linkResponse](t, w); resp.Slug != "Free123" {
		t.Errorf("slug = %q, want the second candidate", resp.Slug)
	}

	next = func() (string, error) { return "Taken12", nil }
	if w := s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil); w.Code != http.StatusInternalServerError {
		t.Errorf("every candidate taken: status = %d, want 500", w.Code)
	}
}

func TestLookupAndClaim(t *testing.T) {
	s := newTestServer(testOptions{})
	created := decode[linkResponse](t, s.do(t, 0, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	tokens := map[string][]string{"claim_tokens": {created.ClaimToken, "unknown-token"}}

	s.queries.counters[created.ID] = 4
	found := decode[[]linkResponse](t, s.do(t, 0, "POST", "/api/v1/links/lookup", tokens, nil))
	if len(found) != 1 || found[0].ID != created.ID || found[0].ClaimToken != "" {
		t.Fatalf("lookup = %+v, want the created link without its token", found)
	}
	if found[0].ClickCount != 4 {
		t.Errorf("click_count = %d, want 4 from link_counters", found[0].ClickCount)
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
	s := newTestServer(testOptions{})
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
	s.queries.breakdown = []db.ListClickDimensionsByLinkRow{
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

func TestStatsReportsAreCapped(t *testing.T) {
	s := newTestServer(testOptions{maxReports: 1})
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	path := "/api/v1/links/" + strconv.FormatInt(created.ID, 10) + "/stats"
	gate := make(chan struct{})
	s.queries.mu.Lock()
	s.queries.statsGate = gate
	s.queries.mu.Unlock()

	codes := make(chan int, 2)
	go func() { codes <- s.do(t, 1, "GET", path, nil, nil).Code }()
	for s.queries.statsCalls() == 0 {
		time.Sleep(time.Millisecond)
	}
	go func() { codes <- s.do(t, 1, "GET", path, nil, nil).Code }()
	time.Sleep(50 * time.Millisecond)
	if n := s.queries.statsCalls(); n != 1 {
		t.Fatalf("%d reports reached the database at once, want 1", n)
	}
	close(gate)
	for range 2 {
		if code := <-codes; code != http.StatusOK {
			t.Errorf("status = %d, want 200 once the first report finished", code)
		}
	}
}

func TestStatsMergesRollupsAndRawClicks(t *testing.T) {
	s := newTestServer(testOptions{})
	created := decode[linkResponse](t, s.do(t, 1, "POST", "/api/v1/links", map[string]string{"target_url": "https://example.com"}, nil))
	path := "/api/v1/links/" + strconv.FormatInt(created.ID, 10) + "/stats?days=7"
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }

	// Today is 2026-09-21. The 15th–19th are rolled up; the 20th is not, so raw clicks cover the 20th and 21st.
	s.queries.rolledDays = []time.Time{day(15), day(16), day(17), day(18), day(19)}
	s.queries.rollupTotals = db.GetRollupTotalsByLinkRow{Clicks: 40, UniqueVisitors: 30, BotClicks: 4}
	s.queries.rollupDaily = []db.ListRollupDailyByLinkRow{{Day: day(16), Clicks: 40, UniqueVisitors: 30}}
	s.queries.rollupDims = []db.ListRollupDimensionsByLinkRow{
		{Dimension: "sources", Name: "Google", Clicks: 25},
		{Dimension: "sources", Name: "X (Twitter)", Clicks: 15},
	}
	s.queries.totals = db.GetClickTotalsByLinkRow{Clicks: 5, UniqueVisitors: 3, BotClicks: 1}
	s.queries.daily = []db.ListDailyClicksByLinkRow{{Day: day(21), Clicks: 5, UniqueVisitors: 3}}
	s.queries.breakdown = []db.ListClickDimensionsByLinkRow{{Dimension: "sources", Name: "X (Twitter)", Clicks: 20}}

	stats := decode[linkStatsResponse](t, s.do(t, 1, "GET", path, nil, nil))

	if stats.Clicks != 45 || stats.UniqueVisitors != 33 || stats.BotClicks != 5 {
		t.Errorf("totals = %d/%d/%d, want rollups plus raw 45/33/5", stats.Clicks, stats.UniqueVisitors, stats.BotClicks)
	}
	if got := s.queries.statsArgs[len(s.queries.statsArgs)-1]; !got.Equal(day(20)) {
		t.Errorf("raw clicks read from %s, want 2026-09-20 (first day without a rollup)", got)
	}
	if w := s.queries.rollupWindows[len(s.queries.rollupWindows)-1]; !w.Since.Equal(day(15)) || !w.Until.Equal(day(20)) {
		t.Errorf("rollup window = %s..%s, want 2026-09-15..2026-09-20", w.Since, w.Until)
	}
	if stats.Daily[1].Clicks != 40 || stats.Daily[6].Clicks != 5 {
		t.Errorf("daily = %+v, want 40 on the 16th and 5 on the 21st", stats.Daily)
	}
	if got := stats.Breakdown["sources"]; len(got) != 2 || got[0].Name != "X (Twitter)" || got[0].Clicks != 35 || got[1].Clicks != 25 {
		t.Errorf("sources = %+v, want X 35 (15 rolled + 20 raw) then Google 25", got)
	}
}
