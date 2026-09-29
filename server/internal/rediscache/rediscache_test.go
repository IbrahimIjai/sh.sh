package rediscache

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

func newStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	store, err := Open("redis://" + server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, server
}

func TestFillGetAndRevoke(t *testing.T) {
	store, server := newStore(t)
	ctx := context.Background()
	target := service.RedirectTarget{
		ID: 7, Slug: "abc", TargetURL: "https://example.com", Title: "Example",
		CreatedAt: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		ExpiresAt: sql.NullTime{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Valid: true},
	}

	if _, _, hit, err := store.Get(ctx, "abc"); err != nil || hit {
		t.Fatalf("empty cache: hit=%v err=%v", hit, err)
	}
	if err := store.Fill(ctx, "abc", target); err != nil {
		t.Fatal(err)
	}
	got, found, hit, err := store.Get(ctx, "abc")
	if err != nil || !hit || !found || got != target {
		t.Fatalf("after fill got %+v found=%v hit=%v err=%v", got, found, hit, err)
	}
	if ttl := server.TTL(linkPrefix + "abc"); ttl <= 0 || ttl > linkTTL {
		t.Errorf("link TTL = %s, want (0, %s]", ttl, linkTTL)
	}

	if err := store.Revoke(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if _, found, hit, _ := store.Get(ctx, "abc"); !hit || found {
		t.Fatalf("after revoke found=%v hit=%v, want a revoked hit", found, hit)
	}
	// A fill that raced the revocation must not bring the link back.
	if err := store.Fill(ctx, "abc", target); err != nil {
		t.Fatal(err)
	}
	if _, found, _, _ := store.Get(ctx, "abc"); found {
		t.Fatal("fill after revoke restored the link")
	}
}

func TestInvalidationsReachSubscribers(t *testing.T) {
	store, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	forgotten := make(chan string, 2)
	go store.Subscribe(ctx, func(slug string) { forgotten <- slug })

	deadline := time.After(2 * time.Second)
	sent := map[string]bool{}
	for len(sent) < 2 {
		store.Announce(ctx, "new-link")
		store.Revoke(ctx, "old-link")
		select {
		case slug := <-forgotten:
			sent[slug] = true
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("received %v, want new-link and old-link", sent)
		}
	}
}

func TestAllowCountsPerWindow(t *testing.T) {
	store, server := newStore(t)
	ctx := context.Background()
	for i := range 3 {
		allowed, err := store.Allow(ctx, "creator", 3, time.Hour)
		if err != nil || !allowed {
			t.Fatalf("event %d: allowed=%v err=%v", i+1, allowed, err)
		}
	}
	if allowed, _ := store.Allow(ctx, "creator", 3, time.Hour); allowed {
		t.Fatal("fourth event in the window was allowed")
	}
	if allowed, _ := store.Allow(ctx, "someone-else", 3, time.Hour); !allowed {
		t.Fatal("limits must be per key")
	}
	for _, key := range server.Keys() {
		if server.TTL(key) <= 0 {
			t.Errorf("key %s has no TTL", key)
		}
	}
}

func TestRequestDeadlineBoundsAStalledRedis(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("set TEST_REDIS_URL to pause a real Redis for 500 ms")
	}
	store, err := Open(url)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.client.Do(context.Background(), "CLIENT", "PAUSE", "500", "ALL").Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	store.Get(ctx, "stalled")
	if took := time.Since(began); took > 200*time.Millisecond {
		t.Fatalf("a 50 ms lookup took %s while Redis was stalled", took)
	}
}
