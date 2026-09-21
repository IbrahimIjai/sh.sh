package analytics

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type ClickStore interface {
	RecordClicks(ctx context.Context, arg db.RecordClicksParams) error
}

type RecorderConfig struct {
	BufferSize int           // clicks waiting to be written; more are dropped. Default 10000.
	BatchSize  int           // clicks per insert. Default 500.
	FlushEvery time.Duration // how long a partial batch may wait. Default 1s.
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

	r := &Recorder{
		store:  store,
		cfg:    cfg,
		events: make(chan Click, cfg.BufferSize),
		quit:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *Recorder) Record(click Click) {
	select {
	case r.events <- click:
	default:
		r.dropped.Add(1)
	}
}

// Close writes out everything already buffered. Call it after the HTTP server has stopped
// taking requests.
func (r *Recorder) Close(ctx context.Context) error {
	r.closeOnce.Do(func() { close(r.quit) })
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) run() {
	defer close(r.done)

	ticker := time.NewTicker(r.cfg.FlushEvery)
	defer ticker.Stop()

	batch := make([]Click, 0, r.cfg.BatchSize)
	flush := func() {
		if len(batch) > 0 {
			r.write(batch)
			batch = batch[:0]
		}
		if dropped := r.dropped.Swap(0); dropped > 0 {
			log.Printf("click recorder: buffer full, dropped %d clicks", dropped)
		}
	}

	for {
		select {
		case click := <-r.events:
			batch = append(batch, click)
			if len(batch) >= r.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-r.quit:
			for {
				select {
				case click := <-r.events:
					batch = append(batch, click)
					if len(batch) >= r.cfg.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (r *Recorder) write(batch []Click) {
	params := db.RecordClicksParams{}
	for _, c := range batch {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.store.RecordClicks(ctx, params); err != nil {
		log.Printf("click recorder: failed to write %d clicks: %v", len(batch), err)
	}
}
