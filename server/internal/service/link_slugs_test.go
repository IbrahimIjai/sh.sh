package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/analytics"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

func slugTestService(t *testing.T, conn *sql.DB) (*LinkService, db.Querier, []int64) {
	t.Helper()
	var users []int64
	for _, email := range []string{"owner@example.com", "other@example.com"} {
		var id int64
		if err := conn.QueryRow("INSERT INTO users (email, updated_at) VALUES ($1, now()) RETURNING id", email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
	}
	queries := db.New(conn)
	links := NewLinkService(queries, LinkConfig{
		Hasher:    analytics.NewHasher("test-secret"),
		Redirects: NewRedirectService(queries, RedirectConfig{}),
	})
	return links, queries, users
}

func TestLinkSlugsInPostgres(t *testing.T) {
	conn := maintenanceTestDatabase(t)
	ctx := context.Background()
	links, queries, users := slugTestService(t, conn)
	owner, other := users[0], users[1]

	create := func(userID int64, slug string) (db.Link, error) {
		created, err := links.Create(ctx, CreateLinkInput{UserID: userID, SignedIn: true, TargetURL: "https://example.com/" + slug, Slug: slug})
		return created.Link, err
	}
	resolves := func(slug string, want int64) {
		t.Helper()
		link, err := queries.GetLinkBySlug(ctx, slug)
		if err != nil || link.ID != want {
			t.Errorf("GetLinkBySlug(%q) = link %d, %v; want link %d", slug, link.ID, err, want)
		}
	}

	random, err := create(owner, "")
	if err != nil {
		t.Fatal(err)
	}
	brand, err := create(other, "EGlobal")
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"EGlobal", "eglobal", random.Slug} {
		if _, err := create(owner, slug); !errors.Is(err, ErrSlugTaken) {
			t.Errorf("create %q: err = %v, want ErrSlugTaken", slug, err)
		}
	}

	long := strings.Repeat("eglobaltech-contacts-", 7)[:128]
	changed, err := links.ChangeSlug(ctx, owner, random.ID, long)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Link.Slug != long || !slices.Equal(changed.PreviousSlugs, []string{random.Slug}) {
		t.Errorf("after change: slug = %q, previous = %v", changed.Link.Slug, changed.PreviousSlugs)
	}
	resolves(long, random.ID)
	resolves(random.Slug, random.ID)

	for _, slug := range []string{"EGlobal", "eglobal"} {
		if _, err := links.ChangeSlug(ctx, owner, random.ID, slug); !errors.Is(err, ErrSlugTaken) {
			t.Errorf("change to %q: err = %v, want ErrSlugTaken", slug, err)
		}
	}
	if _, err := links.ChangeSlug(ctx, other, random.ID, "stolen"); !errors.Is(err, ErrLinkNotFound) {
		t.Errorf("another user's change: err = %v, want ErrLinkNotFound", err)
	}
	for _, slug := range []string{random.Slug, strings.ToUpper(long)} {
		if _, err := create(other, slug); !errors.Is(err, ErrSlugTaken) {
			t.Errorf("create %q after the change: err = %v, want ErrSlugTaken", slug, err)
		}
	}

	back, err := links.ChangeSlug(ctx, owner, random.ID, random.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if back.Link.Slug != random.Slug || !slices.Equal(back.PreviousSlugs, []string{long}) {
		t.Errorf("back to the old slug: slug = %q, previous = %v", back.Link.Slug, back.PreviousSlugs)
	}
	resolves(long, random.ID)
	changes, err := queries.CountSlugChangesSince(ctx, db.CountSlugChangesSinceParams{UserID: owner, Since: time.Now().Add(-time.Hour)})
	if err != nil || changes != 2 {
		t.Errorf("changes = %d, %v; want 2", changes, err)
	}

	taken, _, err := links.Takedown(ctx, []string{long}, "")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(taken)
	want := []string{long, random.Slug}
	slices.Sort(want)
	if !slices.Equal(taken, want) {
		t.Errorf("takedown by the old slug = %v, want %v", taken, want)
	}
	for _, slug := range want {
		if _, err := queries.GetLinkBySlug(ctx, slug); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetLinkBySlug(%q) after takedown: err = %v, want no rows", slug, err)
		}
	}
	resolves("EGlobal", brand.ID)
}

func TestConcurrentClaimsOnOneSlug(t *testing.T) {
	conn := maintenanceTestDatabase(t)
	ctx := context.Background()
	links, _, users := slugTestService(t, conn)

	var existing []db.Link
	for range 4 {
		created, err := links.Create(ctx, CreateLinkInput{UserID: users[0], SignedIn: true, TargetURL: "https://example.com"})
		if err != nil {
			t.Fatal(err)
		}
		existing = append(existing, created.Link)
	}

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			if i < 4 {
				_, err := links.ChangeSlug(ctx, users[0], existing[i].ID, "Contested")
				results <- err
				return
			}
			_, err := links.Create(ctx, CreateLinkInput{UserID: users[1], SignedIn: true, TargetURL: "https://example.org", Slug: []string{"Contested", "contested"}[i%2]})
			results <- err
		})
	}
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrSlugTaken):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Errorf("%d requests got the slug or a look-alike, want exactly 1", won)
	}
}

func TestLinkSlugsBackfill(t *testing.T) {
	conn := maintenanceTestDatabase(t)
	contents, err := os.ReadFile("../../db/migrations/00009_link_slugs.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, _ := strings.Cut(string(contents), "-- +goose Down")
	if _, err := conn.Exec(down); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	for _, slug := range []string{"Abc1234", "my-brand", "My-Brand", "MyStore"} {
		if _, err := conn.Exec("INSERT INTO links (slug, target_url) VALUES ($1, 'https://example.com')", slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(up); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	custom := map[string]bool{}
	rows, err := conn.Query("SELECT slug, custom FROM link_slugs")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var isCustom bool
		if err := rows.Scan(&slug, &isCustom); err != nil {
			t.Fatal(err)
		}
		custom[slug] = isCustom
	}
	want := map[string]bool{"Abc1234": false, "my-brand": true, "My-Brand": false, "MyStore": false}
	for slug, isCustom := range want {
		got, ok := custom[slug]
		if !ok || got != isCustom {
			t.Errorf("%s: custom = %v (present %v), want %v", slug, got, ok, isCustom)
		}
	}
}
