package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

// blockedLoad starts c.Get(key) with a load that returns value once release is closed.
func blockedLoad(c *TTL[string], key, value string) (started, release, done chan struct{}) {
	started, release, done = make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		c.Get(context.Background(), key, func(context.Context, string) (string, bool, error) {
			close(started)
			<-release
			return value, true, nil
		})
	}()
	return started, release, done
}

func load(value string, found bool) LoadFunc[string] {
	return func(context.Context, string) (string, bool, error) { return value, found, nil }
}

func TestForgetDuringLoadKeepsStaleResultOut(t *testing.T) {
	c := New[string](10, time.Minute, time.Second)
	started, release, done := blockedLoad(c, "a", "old destination")
	<-started
	c.Forget("a")
	close(release)
	<-done

	if v, found, _ := c.Get(context.Background(), "a", load("", false)); found {
		t.Fatalf("stale %q survived Forget", v)
	}
}

func TestOlderLoadCannotOverwriteNewerOne(t *testing.T) {
	c := New[string](10, time.Minute, time.Second)
	started, release, done := blockedLoad(c, "a", "old")
	<-started
	c.Forget("a")
	if v, _, _ := c.Get(context.Background(), "a", load("new", true)); v != "new" {
		t.Fatalf("load after Forget = %q, want new", v)
	}
	close(release)
	<-done

	v, found, _ := c.Get(context.Background(), "a", func(context.Context, string) (string, bool, error) {
		t.Error("want a cache hit for the newer value")
		return "", false, nil
	})
	if !found || v != "new" {
		t.Fatalf("cached %q, want new", v)
	}
	if len(c.loading) != 0 {
		t.Errorf("%d in-flight records left behind", len(c.loading))
	}
}

func TestSetReplacesRememberedMissAndEarlierLoad(t *testing.T) {
	c := New[string](10, time.Minute, time.Minute)
	c.Get(context.Background(), "a", load("", false))
	c.Set("a", "created")
	if v, found, _ := c.Get(context.Background(), "a", load("", false)); !found || v != "created" {
		t.Fatalf("after Set: %q, found=%v; want created", v, found)
	}

	started, release, done := blockedLoad(c, "b", "old")
	<-started
	c.Set("b", "new")
	close(release)
	<-done
	if v, _, _ := c.Get(context.Background(), "b", load("", false)); v != "new" {
		t.Fatalf("a load that began before Set overwrote it with %q", v)
	}
}

func TestConcurrentMissesShareOneLoad(t *testing.T) {
	c := New[string](10, time.Minute, time.Second)
	var loads atomic.Int32
	release := make(chan struct{})
	slow := func(context.Context, string) (string, bool, error) {
		loads.Add(1)
		<-release
		return "A", true, nil
	}

	results := make(chan string, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			v, _, _ := c.Get(context.Background(), "a", slow)
			results <- v
		})
	}
	for c.Stats().Misses < 20 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	close(results)

	for v := range results {
		if v != "A" {
			t.Fatalf("got %q, want A", v)
		}
	}
	if n := loads.Load(); n != 1 {
		t.Fatalf("20 concurrent misses made %d loads, want 1", n)
	}
	if s := c.Stats(); s.Coalesced != 19 {
		t.Errorf("coalesced = %d, want 19", s.Coalesced)
	}
}

func TestServesStaleEntryWhenReloadFails(t *testing.T) {
	c, now := newTestCache(10)
	c.WithStale(time.Minute)
	s := newFakeStore(map[string]string{"a": "A"})
	get(t, c, s, "a")

	s.err = errors.New("database down")
	*now = now.Add(90 * time.Second)
	if v, found, err := c.Get(context.Background(), "a", s.load); err != nil || !found || v != "A" {
		t.Fatalf("within the stale window got %q, %v, %v; want the old entry", v, found, err)
	}
	if c.Stats().Stale != 1 {
		t.Errorf("stale serves = %d, want 1", c.Stats().Stale)
	}

	*now = now.Add(time.Minute)
	if _, _, err := c.Get(context.Background(), "a", s.load); err == nil {
		t.Fatal("past the stale window the load error must surface")
	}
}

func TestWaiterGivesUpWithoutCancellingTheLoad(t *testing.T) {
	c := New[string](10, time.Minute, time.Second)
	started, release, done := blockedLoad(c, "a", "A")
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Get(ctx, "a", load("", false)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter got %v, want context.Canceled", err)
	}
	close(release)
	<-done
	if v, found, _ := c.Get(context.Background(), "a", load("", false)); !found || v != "A" {
		t.Fatalf("after the shared load finished got %q, %v; want A", v, found)
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
