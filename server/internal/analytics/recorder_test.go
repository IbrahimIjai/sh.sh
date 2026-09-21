package analytics

import (
	"context"
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

func (f *fakeClickStore) RecordClicks(_ context.Context, arg db.RecordClicksParams) error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, arg)
	return nil
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

	recorder.Record(Click{LinkID: 1, Source: "Direct"})
	recorder.Record(Click{LinkID: 2, IsBot: true})
	recorder.Record(Click{LinkID: 3})

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
	} {
		if n != len(first.LinkIds) {
			t.Errorf("%s has %d values, want %d", name, n, len(first.LinkIds))
		}
	}
	if first.Sources[0] != "Direct" || !first.IsBots[1] {
		t.Errorf("column values out of order: sources=%v is_bots=%v", first.Sources, first.IsBots)
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
