package analytics

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	mathrand "math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type ClickStore interface {
	RecordClicks(ctx context.Context, arg db.RecordClicksParams) error
}

type RecorderConfig struct {
	BufferSize   int           // clicks waiting to be written; more are dropped. Default 10000.
	BatchSize    int           // clicks per insert. Default 500.
	FlushEvery   time.Duration // how long a partial batch may wait. Default 1s.
	WriteTimeout time.Duration // deadline for each database attempt. Default 10s.
	RetryMin     time.Duration // initial retry delay, with jitter. Default 100ms.
	RetryMax     time.Duration // maximum retry delay. Default 5s.
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
	dropped   atomic.Int64
	ctx       context.Context
	cancel    context.CancelFunc
	err       error // written by run, read only after done closes
}

func NewRecorder(store ClickStore, cfg RecorderConfig) *Recorder {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 10000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
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
	select {
	case <-r.quit:
		return
	default:
	}
	select {
	case r.events <- click:
	default:
		r.dropped.Add(1)
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

	ticker := time.NewTicker(r.cfg.FlushEvery)
	defer ticker.Stop()

	batch := make([]Click, 0, r.cfg.BatchSize)
	flush := func() bool {
		if len(batch) > 0 {
			if err := r.write(batch); err != nil {
				r.err = fmt.Errorf("%d clicks unconfirmed at shutdown: %w", len(batch)+len(r.events), err)
				log.Printf("click recorder: %v", r.err)
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
	// Generate once, outside the retry loop. The database receipt makes a replay
	// safe even when an attempt committed but its acknowledgement never arrived.
	params := db.RecordClicksParams{BatchID: rand.Text()}
	for _, c := range batch {
		params.CreatedAts = append(params.CreatedAts, c.CreatedAt)
		params.LinkIds = append(params.LinkIds, c.LinkID)
		params.VisitorHashes = append(params.VisitorHashes, c.VisitorHash)
		params.UserAgents = append(params.UserAgents, c.UserAgent)
		params.RefererHosts = append(params.RefererHosts, c.RefererHost)
		params.Sources = append(params.Sources, c.Source)
		params.UtmSources = append(params.UtmSources, c.UTMSource)
		params.UtmMediums = append(params.UtmMediums, c.UTMMedium)
		params.UtmCampaigns = append(params.UtmCampaigns, c.UTMCampaign)
		params.DeviceTypes = append(params.DeviceTypes, c.DeviceType)
		params.Browsers = append(params.Browsers, c.Browser)
		params.Oses = append(params.Oses, c.OS)
		params.Countries = append(params.Countries, c.Country)
		params.Cities = append(params.Cities, c.City)
		params.IsBots = append(params.IsBots, c.IsBot)
	}

	delay := r.cfg.RetryMin
	for attempt := 1; ; attempt++ {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.ctx, r.cfg.WriteTimeout)
		err := r.store.RecordClicks(ctx, params)
		cancel()
		if err == nil {
			if attempt > 1 {
				log.Printf("click recorder: recovered batch of %d clicks after %d attempts", len(batch), attempt)
			}
			return nil
		}
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
