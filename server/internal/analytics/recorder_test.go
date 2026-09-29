package analytics

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type fakeClickStore struct {
	mu      sync.Mutex
	batches []db.RecordClicksParams
	block   chan struct{}
}

func (f *fakeClickStore) RecordClicks(_ context.Context, arg db.RecordClicksParams) (int64, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, arg)
	return int64(len(arg.LinkIds)), nil
}

func (f *fakeClickStore) linkIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []int64
	for _, batch := range f.batches {
		ids = append(ids, batch.LinkIds...)
	}
	return ids
}

func TestRecorderBatchesAndDrainsOnClose(t *testing.T) {
	store := &fakeClickStore{}
	recorder := NewRecorder(store, RecorderConfig{BatchSize: 2, FlushEvery: time.Hour})

	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	recorder.Record(Click{LinkID: 1, Source: "Direct", CreatedAt: at})
	recorder.Record(Click{LinkID: 2, IsBot: true, CreatedAt: at.Add(time.Second)})
	recorder.Record(Click{LinkID: 3, CreatedAt: at.Add(2 * time.Second)})

	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := store.linkIDs(); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("recorded link ids = %v, want [1 2 3]", got)
	}
	first := store.batches[0]
	if len(store.batches) != 2 || len(first.LinkIds) != 2 {
		t.Fatalf("batches = %d (first has %d), want 2 batches with the first full", len(store.batches), len(first.LinkIds))
	}
	// Every column array must line up with link_ids, or unnest pairs values with the wrong click.
	for name, n := range map[string]int{
		"visitor_hashes": len(first.VisitorHashes), "sources": len(first.Sources), "is_bots": len(first.IsBots),
		"countries": len(first.Countries), "cities": len(first.Cities), "oses": len(first.Oses),
		"created_ats": len(first.CreatedAts), "event_ids": len(first.EventIds),
	} {
		if n != len(first.LinkIds) {
			t.Errorf("%s has %d values, want %d", name, n, len(first.LinkIds))
		}
	}
	if first.Sources[0] != "Direct" || !first.IsBots[1] {
		t.Errorf("column values out of order: sources=%v is_bots=%v", first.Sources, first.IsBots)
	}
}

type clickStoreFunc func(context.Context, db.RecordClicksParams) (int64, error)

func (f clickStoreFunc) RecordClicks(ctx context.Context, arg db.RecordClicksParams) (int64, error) {
	return f(ctx, arg)
}

func TestRecorderRetriesSameBatchBeforeNextBatch(t *testing.T) {
	var attempts []db.RecordClicksParams
	store := clickStoreFunc(func(_ context.Context, arg db.RecordClicksParams) (int64, error) {
		attempts = append(attempts, arg)
		if len(attempts) <= 2 {
			return 0, errors.New("database temporarily unavailable")
		}
		return int64(len(arg.LinkIds)), nil
	})
	recorder := NewRecorder(store, RecorderConfig{BatchSize: 2, FlushEvery: time.Hour, RetryMin: time.Millisecond, RetryMax: 2 * time.Millisecond})
	at := time.Date(2026, 9, 21, 23, 59, 59, 0, time.UTC)
	recorder.Record(Click{LinkID: 1, CreatedAt: at})
	recorder.Record(Click{LinkID: 2, IsBot: true, CreatedAt: at})
	recorder.Record(Click{LinkID: 3})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recorder.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 4 {
		t.Fatalf("attempts = %d, want 4", len(attempts))
	}
	if len(attempts[0].EventIds) != 2 || attempts[0].EventIds[0] == "" || attempts[0].EventIds[0] == attempts[0].EventIds[1] {
		t.Fatalf("event IDs = %q, want two distinct IDs", attempts[0].EventIds)
	}
	if !reflect.DeepEqual(attempts[0], attempts[1]) || !reflect.DeepEqual(attempts[0], attempts[2]) {
		t.Fatal("retry changed the event IDs, data, or timestamp")
	}
	if !attempts[0].CreatedAts[0].Equal(at) || attempts[3].CreatedAts[0].IsZero() {
		t.Fatal("original click time was not retained")
	}
	if !reflect.DeepEqual(attempts[3].LinkIds, []int64{3}) || slices.Contains(attempts[0].EventIds, attempts[3].EventIds[0]) {
		t.Fatal("next batch must have its own event IDs and run after recovery")
	}
}

func TestRecorderCloseCancelsWriteAndBackoff(t *testing.T) {
	for _, waitingOnDatabase := range []bool{false, true} {
		t.Run(map[bool]string{false: "backoff", true: "database"}[waitingOnDatabase], func(t *testing.T) {
			started := make(chan struct{})
			store := clickStoreFunc(func(ctx context.Context, _ db.RecordClicksParams) (int64, error) {
				close(started)
				if waitingOnDatabase {
					<-ctx.Done()
					return 0, ctx.Err()
				}
				return 0, errors.New("offline")
			})
			recorder := NewRecorder(store, RecorderConfig{BatchSize: 1, WriteTimeout: time.Hour, RetryMin: time.Hour})
			recorder.Record(Click{LinkID: 1})
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("write did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := recorder.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Close = %v, want deadline exceeded", err)
			}
			select {
			case <-recorder.done:
			case <-time.After(time.Second):
				t.Fatal("recorder leaked after shutdown deadline")
			}
			if err := recorder.Close(context.Background()); !errors.Is(err, context.Canceled) {
				t.Fatalf("later Close = %v, must report the unconfirmed batch", err)
			}
		})
	}
}

func TestRecorderRetriesTimedOutAttempt(t *testing.T) {
	attempts := 0
	store := clickStoreFunc(func(ctx context.Context, _ db.RecordClicksParams) (int64, error) {
		attempts++
		if attempts == 1 {
			<-ctx.Done()
			return 0, ctx.Err()
		}
		return 1, nil
	})
	recorder := NewRecorder(store, RecorderConfig{WriteTimeout: 10 * time.Millisecond, RetryMin: time.Millisecond})
	recorder.Record(Click{LinkID: 1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recorder.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRecorderFlushesOnTimer(t *testing.T) {
	store := &fakeClickStore{}
	recorder := NewRecorder(store, RecorderConfig{BatchSize: 100, FlushEvery: 10 * time.Millisecond})
	defer recorder.Close(context.Background())

	recorder.Record(Click{LinkID: 7})

	deadline := time.Now().Add(2 * time.Second)
	for len(store.linkIDs()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("click was not flushed by the timer")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRecorderDropsWhenFull(t *testing.T) {
	store := &fakeClickStore{block: make(chan struct{})}
	recorder := NewRecorder(store, RecorderConfig{BufferSize: 1, BatchSize: 1, FlushEvery: time.Hour})

	// The first click is picked up and blocks in the store, the second fills the buffer, and
	// Record must not block on the third.
	done := make(chan struct{})
	go func() {
		for i := range 10 {
			recorder.Record(Click{LinkID: int64(i)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record blocked with a full buffer")
	}
	if recorder.dropped.Load() == 0 {
		t.Error("expected dropped clicks to be counted")
	}

	close(store.block)
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
