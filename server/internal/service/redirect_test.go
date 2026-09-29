package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type deletedLinks struct{ db.Querier }

func (deletedLinks) GetLinkBySlug(context.Context, string) (db.Link, error) {
	return db.Link{}, sql.ErrNoRows
}

// staleShared still holds the link and can't be written to.
type staleShared struct {
	SharedCache
	target RedirectTarget
}

func (c staleShared) Get(context.Context, string) (RedirectTarget, bool, bool, error) {
	return c.target, true, true, nil
}

func (staleShared) Revoke(context.Context, string) error { return errors.New("redis write failed") }

func TestFailedSharedRevokeStillStopsThisInstance(t *testing.T) {
	shared := staleShared{target: RedirectTarget{ID: 1, Slug: "removed", TargetURL: "https://example.com"}}
	redirects := NewRedirectService(deletedLinks{}, RedirectConfig{CacheSize: 10, CacheTTL: time.Minute, Shared: shared})
	if _, found, err := redirects.Resolve(context.Background(), "removed"); err != nil || !found {
		t.Fatal("the link should resolve from Redis before the delete")
	}
	if err := redirects.Revoke(context.Background(), "removed"); err == nil {
		t.Error("Revoke hid the failed Redis write")
	}
	if _, found, err := redirects.Resolve(context.Background(), "removed"); err != nil || found {
		t.Fatalf("found=%v err=%v; the stale Redis copy brought a deleted link back", found, err)
	}
}

type downShared struct {
	SharedCache
	gets *int
}

func (c downShared) Get(context.Context, string) (RedirectTarget, bool, bool, error) {
	*c.gets++
	return RedirectTarget{}, false, false, errors.New("connection refused")
}

func (downShared) Fill(context.Context, string, RedirectTarget) error {
	return errors.New("connection refused")
}

func TestRedisFailureIsNotPaidByEveryRequest(t *testing.T) {
	gets := 0
	redirects := NewRedirectService(deletedLinks{}, RedirectConfig{CacheSize: 10, CacheTTL: time.Minute, Shared: downShared{gets: &gets}})
	for _, slug := range []string{"a", "b", "c"} {
		if _, _, err := redirects.Resolve(context.Background(), slug); err != nil {
			t.Fatal(err)
		}
	}
	if gets != 1 {
		t.Fatalf("Redis was tried %d times, want once before backing off", gets)
	}
}
