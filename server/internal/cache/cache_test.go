package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	values map[string]string
	loads  map[string]int
	err    error
}

func newFakeStore(values map[string]string) *fakeStore {
	return &fakeStore{values: values, loads: map[string]int{}}
}

func (s *fakeStore) load(_ context.Context, key string) (string, bool, error) {
	s.loads[key]++
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.values[key]
	return v, ok, nil
}

func newTestCache(capacity int) (*TTL[string], *time.Time) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	c := New[string](capacity, time.Minute, 5*time.Second)
	c.now = func() time.Time { return now }
	return c, &now
}

func get(t *testing.T, c *TTL[string], s *fakeStore, key string) (string, bool) {
	t.Helper()
	v, found, err := c.Get(context.Background(), key, s.load)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return v, found
}

func TestHitsAndMissesAreCached(t *testing.T) {
	c, _ := newTestCache(10)
	s := newFakeStore(map[string]string{"a": "A"})

	for range 3 {
		if v, found := get(t, c, s, "a"); !found || v != "A" {
			t.Fatalf("a = %q, %v", v, found)
		}
		if _, found := get(t, c, s, "missing"); found {
			t.Fatal("missing was found")
		}
	}
	if s.loads["a"] != 1 || s.loads["missing"] != 1 {
		t.Fatalf("loads = %v, want one each", s.loads)
	}
}

func TestEntriesExpire(t *testing.T) {
	c, now := newTestCache(10)
	s := newFakeStore(map[string]string{"a": "A"})
	get(t, c, s, "a")
	get(t, c, s, "missing")

	*now = now.Add(5 * time.Second)
	get(t, c, s, "a")
	get(t, c, s, "missing")
	if s.loads["a"] != 1 || s.loads["missing"] != 2 {
		t.Fatalf("after 5s loads = %v, want a=1 missing=2", s.loads)
	}

	*now = now.Add(time.Minute)
	get(t, c, s, "a")
	if s.loads["a"] != 2 {
		t.Fatalf("after 1m loads[a] = %d, want 2", s.loads["a"])
	}
}

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	c, _ := newTestCache(2)
	s := newFakeStore(map[string]string{"a": "A", "b": "B", "c": "C"})
	get(t, c, s, "a")
	get(t, c, s, "b")
	get(t, c, s, "a")
	get(t, c, s, "c")

	get(t, c, s, "a")
	get(t, c, s, "b")
	if s.loads["a"] != 1 || s.loads["b"] != 2 {
		t.Fatalf("loads = %v, want b evicted and a kept", s.loads)
	}
}

func TestForget(t *testing.T) {
	c, _ := newTestCache(10)
	s := newFakeStore(map[string]string{})
	get(t, c, s, "a")

	s.values["a"] = "A"
	c.Forget("a")
	if v, found := get(t, c, s, "a"); !found || v != "A" {
		t.Fatalf("after Forget a = %q, %v", v, found)
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	c, _ := newTestCache(10)
	s := newFakeStore(map[string]string{"a": "A"})
	s.err = errors.New("database down")
	if _, _, err := c.Get(context.Background(), "a", s.load); err == nil {
		t.Fatal("want error")
	}

	s.err = nil
	if v, found := get(t, c, s, "a"); !found || v != "A" {
		t.Fatalf("after recovery a = %q, %v", v, found)
	}
}

func TestDisabledCacheAlwaysLoads(t *testing.T) {
	c := New[string](0, time.Minute, time.Second)
	if c != nil {
		t.Fatal("zero capacity should disable the cache")
	}
	s := newFakeStore(map[string]string{"a": "A"})
	get(t, c, s, "a")
	get(t, c, s, "a")
	c.Forget("a")
	if s.loads["a"] != 2 {
		t.Fatalf("loads[a] = %d, want 2", s.loads["a"])
	}
}
