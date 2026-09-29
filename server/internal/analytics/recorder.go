package analytics

import (
	"context"
	"fmt"
	"log"
	mathrand "math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

// ClickStore stores a batch of clicks and returns how many were new; clicks already stored are skipped.
type ClickStore interface {
	RecordClicks(ctx context.Context, arg db.RecordClicksParams) (int64, error)
}

type RecorderConfig struct {
	BufferSize   int           // clicks waiting to be written; more are dropped. Default 10000.
	BatchSize    int           // clicks per insert. Default 500.
	Writers      int           // batches written in parallel. Default 1.
	FlushEvery   time.Duration // how long a partial batch may wait. Default 1s.
	WriteTimeout time.Duration // deadline for each database attempt. Default 10s.
	RetryMin     time.Duration // initial retry delay, with jitter. Default 100ms.
	RetryMax     time.Duration // maximum retry delay. Default 5s.
}

// RecorderStats are totals since the recorder started.
type RecorderStats struct {
	Written      uint64
	Duplicates   uint64 // written clicks that were already stored, from retried batches
	Dropped      uint64
	FailedWrites uint64
	Batches      uint64
}

// Recorder writes clicks in the background so redirects never wait on the database.
// Record is non-blocking: if the buffer is full the click is dropped and counted in the logs,
// because a slow database must not slow down redirects.
type Recorder struct {
	store     ClickStore
	cfg       RecorderConfig
	events    chan Click
	quit      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	dropped   atomic.Int64 // since the last log line
	ctx       context.Context
	cancel    context.CancelFunc

	written      atomic.Uint64
	duplicates   atomic.Uint64
	droppedTotal atomic.Uint64
	failedWrites atomic.Uint64
	batches      atomic.Uint64

	errMu sync.Mutex
	err   error // read only after done closes
}

func NewRecorder(store ClickStore, cfg RecorderConfig) *Recorder {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 10000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}
	if cfg.Writers <= 0 {
		cfg.Writers = 1
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.RetryMin <= 0 {
		cfg.RetryMin = 100 * time.Millisecond
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = 5 * time.Second
	}
	if cfg.RetryMax < cfg.RetryMin {
		cfg.RetryMax = cfg.RetryMin
	}
	ctx, cancel := context.WithCancel(context.Background())

	r := &Recorder{
		store:  store,
		cfg:    cfg,
		events: make(chan Click, cfg.BufferSize),
		quit:   make(chan struct{}),
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}
	go r.run()
	return r
}

func (r *Recorder) Record(click Click) {
	if click.CreatedAt.IsZero() {
		click.CreatedAt = time.Now()
	}
	click.CreatedAt = click.CreatedAt.Truncate(time.Microsecond)
	if click.EventID == "" {
		click.EventID = NewEventID()
	}
	select {
	case <-r.quit:
		return
	default:
	}
	select {
	case r.events <- click:
	default:
		r.dropped.Add(1)
		r.droppedTotal.Add(1)
	}
}

// QueueDepth is the number of clicks waiting in the buffer.
func (r *Recorder) QueueDepth() int {
	return len(r.events)
}

func (r *Recorder) Stats() RecorderStats {
	return RecorderStats{
		Written:      r.written.Load(),
		Duplicates:   r.duplicates.Load(),
		Dropped:      r.droppedTotal.Load(),
		FailedWrites: r.failedWrites.Load(),
		Batches:      r.batches.Load(),
	}
}

// Close writes out everything already buffered. Call it after the HTTP server has stopped
// taking requests. Failed writes keep retrying until they succeed or ctx expires;
// expiry cancels the database attempt and returns an error, rather than claiming a drain.
func (r *Recorder) Close(ctx context.Context) error {
	r.closeOnce.Do(func() { close(r.quit) })
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		r.cancel()
		return ctx.Err()
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	defer r.cancel()

	var wg sync.WaitGroup
	for range r.cfg.Writers {
		wg.Go(r.work)
	}
	wg.Wait()
}

func (r *Recorder) fail(err error) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *Recorder) work() {
	ticker := time.NewTicker(r.cfg.FlushEvery)
	defer ticker.Stop()

	batch := make([]Click, 0, r.cfg.BatchSize)
	flush := func() bool {
		if len(batch) > 0 {
			if err := r.write(batch); err != nil {
				err = fmt.Errorf("%d clicks unconfirmed at shutdown: %w", len(batch)+len(r.events), err)
				log.Printf("click recorder: %v", err)
				r.fail(err)
				return false
			}
			batch = batch[:0]
		}
		if dropped := r.dropped.Swap(0); dropped > 0 {
			log.Printf("click recorder: buffer full, dropped %d clicks", dropped)
		}
		return true
	}

	for {
		select {
		case click := <-r.events:
			batch = append(batch, click)
			if len(batch) >= r.cfg.BatchSize {
				if !flush() {
					return
				}
			}
		case <-ticker.C:
			if !flush() {
				return
			}
		case <-r.quit:
			for {
				select {
				case click := <-r.events:
					batch = append(batch, click)
					if len(batch) >= r.cfg.BatchSize {
						if !flush() {
							return
						}
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (r *Recorder) write(batch []Click) error {
	// The event IDs make a retry safe even when an attempt committed but its acknowledgement
	// never arrived.
	params := BatchParams(batch)

	delay := r.cfg.RetryMin
	for attempt := 1; ; attempt++ {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.ctx, r.cfg.WriteTimeout)
		inserted, err := r.store.RecordClicks(ctx, params)
		cancel()
		if err == nil {
			r.written.Add(uint64(len(batch)))
			r.duplicates.Add(uint64(len(batch) - int(inserted)))
			r.batches.Add(1)
			if attempt > 1 {
				log.Printf("click recorder: recovered batch of %d clicks after %d attempts", len(batch), attempt)
			}
			return nil
		}
		r.failedWrites.Add(1)
		if r.ctx.Err() != nil {
			return fmt.Errorf("last write: %v: %w", err, r.ctx.Err())
		}
		// Equal jitter prevents recovering replicas from reconnecting in lockstep.
		wait := delay/2 + time.Duration(mathrand.Int64N(int64(delay-delay/2)))
		log.Printf("click recorder: write of %d clicks failed (attempt %d); retrying in %s: %v", len(batch), attempt, wait, err)
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-r.ctx.Done():
			timer.Stop()
			return r.ctx.Err()
		}
		if delay >= r.cfg.RetryMax/2 {
			delay = r.cfg.RetryMax
		} else {
			delay *= 2
		}
	}
}
