package clickstream

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

// fakeStore skips event IDs it has already stored, like the real RecordClicks.
type fakeStore struct {
	mu     sync.Mutex
	stored map[string]analytics.Click
	fail   func(db.RecordClicksParams) error
}

func newFakeStore() *fakeStore {
	return &fakeStore{stored: map[string]analytics.Click{}}
}

func (f *fakeStore) RecordClicks(_ context.Context, arg db.RecordClicksParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		if err := f.fail(arg); err != nil {
			return 0, err
		}
	}
	var inserted int64
	for i, id := range arg.EventIds {
		if _, ok := f.stored[id]; ok {
			continue
		}
		f.stored[id] = analytics.Click{
			EventID: id, CreatedAt: arg.CreatedAts[i], LinkID: arg.LinkIds[i], VisitorHash: arg.VisitorHashes[i],
			RefererHost: arg.RefererHosts[i], Source: arg.Sources[i], UTMSource: arg.UtmSources[i],
			UTMMedium: arg.UtmMediums[i], UTMCampaign: arg.UtmCampaigns[i], DeviceType: arg.DeviceTypes[i],
			Browser: arg.Browsers[i], OS: arg.Oses[i], Country: arg.Countries[i], City: arg.Cities[i], IsBot: arg.IsBots[i],
		}
		inserted++
	}
	return inserted, nil
}

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stored)
}

func (f *fakeStore) links() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []int64
	for _, c := range f.stored {
		ids = append(ids, c.LinkID)
	}
	slices.Sort(ids)
	return ids
}

type fakeFallback struct {
	mu     sync.Mutex
	clicks []analytics.Click
}

func (f *fakeFallback) Record(c analytics.Click) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clicks = append(f.clicks, c)
}

func (f *fakeFallback) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clicks)
}

// newTestQueue returns an unstarted queue and a separate client for inspecting Redis.
func newTestQueue(t *testing.T, m *miniredis.Miniredis, store analytics.ClickStore, fallback Fallback, cfg Config) (*Queue, *redis.Client) {
	t.Helper()
	q := newQueue(redis.NewClient(&redis.Options{Addr: m.Addr(), ContextTimeoutEnabled: true}), store, fallback, cfg)
	q.claimEvery = 20 * time.Millisecond
	q.checkEvery = 10 * time.Millisecond
	inspect := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		q.Close(ctx)
		inspect.Close()
	})
	return q, inspect
}

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func streamLength(t *testing.T, client *redis.Client, key string) int64 {
	t.Helper()
	n, err := client.XLen(context.Background(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// appendEntry adds clicks as one entry, as another instance's producer would.
func appendEntry(t *testing.T, client *redis.Client, clicks ...analytics.Click) {
	t.Helper()
	payload, err := encode(clicks)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.XAdd(context.Background(), &redis.XAddArgs{Stream: streamKey, Values: []any{"v", wireVersion, "d", payload}}).Err(); err != nil {
		t.Fatal(err)
	}
}

func click(linkID int64) analytics.Click {
	return analytics.Click{EventID: analytics.NewEventID(), CreatedAt: time.Now().Truncate(time.Microsecond), LinkID: linkID}
}

func TestClicksReachTheStoreThroughTheStream(t *testing.T) {
	m := miniredis.RunT(t)
	store, fallback := newFakeStore(), &fakeFallback{}
	q, inspect := newTestQueue(t, m, store, fallback, Config{})
	q.startProducer()
	q.startConsumers()

	at := time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.UTC)
	full := analytics.Click{LinkID: 1, CreatedAt: at, VisitorHash: "v", RefererHost: "x.com", Source: "X (Twitter)",
		UTMSource: "s", UTMMedium: "m", UTMCampaign: "c", DeviceType: "mobile", Browser: "Safari", OS: "iOS",
		Country: "NG", City: "Lagos"}
	q.Record(full)
	q.Record(analytics.Click{LinkID: 2, CreatedAt: at, IsBot: true})

	eventually(t, "both clicks stored", func() bool { return store.count() == 2 })
	eventually(t, "the stream emptied", func() bool { return streamLength(t, inspect, streamKey) == 0 })
	var got analytics.Click
	for _, c := range store.stored {
		if c.LinkID == 1 {
			got = c
		}
	}
	full.EventID = got.EventID
	full.CreatedAt = at.Truncate(time.Microsecond)
	if !validUUID(got.EventID) || !got.CreatedAt.Equal(full.CreatedAt) {
		t.Fatalf("stored event %q at %v, want a UUID at %v", got.EventID, got.CreatedAt, full.CreatedAt)
	}
	got.CreatedAt = full.CreatedAt
	if got != full {
		t.Errorf("stored %+v\nwant %+v", got, full)
	}
	pending, err := inspect.XPending(context.Background(), streamKey, group).Result()
	if err != nil || pending.Count != 0 {
		t.Errorf("pending = %+v, err = %v; want nothing pending", pending, err)
	}
	if s := q.Stats(); s.Appended != 2 || s.Written != 2 || s.Fallback != 0 || s.Duplicates != 0 {
		t.Errorf("stats = %+v", s)
	}
}

func TestRedisDownSendsClicksToTheFallback(t *testing.T) {
	m := miniredis.RunT(t)
	store, fallback := newFakeStore(), &fakeFallback{}
	q, _ := newTestQueue(t, m, store, fallback, Config{})
	q.startProducer()
	m.Close()

	for i := range 3 {
		q.Record(click(int64(i + 1)))
	}
	eventually(t, "clicks handed to the fallback", func() bool { return fallback.count() == 3 })
	if s := q.Stats(); s.Fallback != 3 || s.Appended != 0 {
		t.Errorf("stats = %+v", s)
	}
}

func TestTakesOverEntriesLeftByACrashedInstance(t *testing.T) {
	m := miniredis.RunT(t)
	store := newFakeStore()
	q, inspect := newTestQueue(t, m, store, &fakeFallback{}, Config{ClaimIdle: 50 * time.Millisecond})
	ctx := context.Background()

	// A crashed instance read two entries and wrote the first to the database, but acknowledged neither.
	if err := inspect.XGroupCreateMkStream(ctx, streamKey, group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	written, unwritten := click(1), click(2)
	appendEntry(t, inspect, written)
	appendEntry(t, inspect, unwritten)
	if err := inspect.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: "crashed", Streams: []string{streamKey, ">"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordClicks(ctx, analytics.BatchParams([]analytics.Click{written})); err != nil {
		t.Fatal(err)
	}

	q.startProducer()
	q.startConsumers()
	eventually(t, "the stream emptied", func() bool { return streamLength(t, inspect, streamKey) == 0 })
	if got := store.links(); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("stored links %v, want each click once", got)
	}
	if s := q.Stats(); s.Claimed != 2 || s.Written != 2 || s.Duplicates != 1 {
		t.Errorf("stats = %+v, want 2 entries taken over and the written click skipped", s)
	}
}

func TestInvalidEntriesGoToDeadLetters(t *testing.T) {
	m := miniredis.RunT(t)
	store := newFakeStore()
	store.fail = func(arg db.RecordClicksParams) error {
		if slices.Contains(arg.LinkIds, 13) {
			return &pq.Error{Code: "22001", Message: "value too long"}
		}
		return nil
	}
	q, inspect := newTestQueue(t, m, store, &fakeFallback{}, Config{})
	ctx := context.Background()
	if err := inspect.XGroupCreateMkStream(ctx, streamKey, group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := inspect.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, Values: []any{"v", wireVersion, "d", "not json"}}).Err(); err != nil {
		t.Fatal(err)
	}
	appendEntry(t, inspect, click(13))
	appendEntry(t, inspect, click(7))

	q.startProducer()
	q.startConsumers()
	eventually(t, "the stream emptied", func() bool { return streamLength(t, inspect, streamKey) == 0 })
	if got := store.links(); !slices.Equal(got, []int64{7}) {
		t.Errorf("stored links %v, want only the valid entry", got)
	}
	dead, err := inspect.XRange(ctx, deadKey, "-", "+").Result()
	if err != nil || len(dead) != 2 {
		t.Fatalf("dead letters = %d, err = %v; want the undecodable and the rejected entry", len(dead), err)
	}
	for _, d := range dead {
		if d.Values["id"] == "" || d.Values["error"] == "" {
			t.Errorf("dead letter %v lacks the original ID or the error", d.Values)
		}
	}
	if s := q.Stats(); s.DeadLetters != 2 || s.Written != 1 {
		t.Errorf("stats = %+v", s)
	}
}

func TestRetryingConsumerKeepsItsEntries(t *testing.T) {
	m := miniredis.RunT(t)
	store := newFakeStore()
	var down atomic.Bool
	down.Store(true)
	store.fail = func(db.RecordClicksParams) error {
		if down.Load() {
			return errors.New("database unavailable")
		}
		return nil
	}
	q, _ := newTestQueue(t, m, store, &fakeFallback{}, Config{ClaimIdle: 100 * time.Millisecond, RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond})
	q.startProducer()
	q.startConsumers()

	q.Record(click(1))
	eventually(t, "a failed write", func() bool { return q.Stats().FailedWrites > 0 })
	time.Sleep(400 * time.Millisecond)
	down.Store(false)
	eventually(t, "the click stored", func() bool { return store.count() == 1 })
	if s := q.Stats(); s.Claimed != 0 || s.Duplicates != 0 {
		t.Errorf("stats = %+v; entries under retry must not be taken over", s)
	}
}

func TestOverBudgetStreamSendsClicksToTheFallback(t *testing.T) {
	m := miniredis.RunT(t)
	store, fallback := newFakeStore(), &fakeFallback{}
	q, _ := newTestQueue(t, m, store, fallback, Config{MaxBytes: 1})
	q.startProducer()
	q.startConsumers()

	eventually(t, "the budget check", func() bool { return q.overBudget.Load() })
	q.Record(click(1))
	eventually(t, "the click handed to the fallback", func() bool { return fallback.count() == 1 })
	if s := q.Stats(); s.Appended != 0 || s.BacklogBytes <= 1 {
		t.Errorf("stats = %+v", s)
	}
}

func TestCloseAppendsBufferedClicks(t *testing.T) {
	m := miniredis.RunT(t)
	fallback := &fakeFallback{}
	q, inspect := newTestQueue(t, m, newFakeStore(), fallback, Config{})
	q.startProducer()
	for i := range 5 {
		q.Record(click(int64(i + 1)))
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	entries, err := inspect.XRange(context.Background(), streamKey, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	appended := 0
	for _, e := range entries {
		clicks, err := decode(e.Values)
		if err != nil {
			t.Fatal(err)
		}
		appended += len(clicks)
	}
	if appended != 5 || fallback.count() != 0 {
		t.Errorf("appended %d clicks and handed %d to the fallback, want all 5 appended", appended, fallback.count())
	}
}

func TestDecodeRejectsInvalidEntries(t *testing.T) {
	good, err := encode([]analytics.Click{click(1)})
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]map[string]any{
		"unknown version": {"v": "2", "d": good},
		"no payload":      {"v": wireVersion},
		"empty batch":     {"v": wireVersion, "d": "[]"},
		"bad event ID":    {"v": wireVersion, "d": `[{"e":"x","t":1,"l":1}]`},
		"no link":         {"v": wireVersion, "d": `[{"e":"0190c8a4-7f3e-4c4a-9d2e-1a2b3c4d5e6f","t":1,"l":0}]`},
	} {
		if _, err := decode(values); err == nil {
			t.Errorf("%s: decoded without error", name)
		}
	}
	if _, err := decode(map[string]any{"v": wireVersion, "d": good}); err != nil {
		t.Errorf("valid entry: %v", err)
	}
}
