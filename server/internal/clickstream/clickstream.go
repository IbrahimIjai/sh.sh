// Package clickstream carries clicks from the API instance that saw them to PostgreSQL through a
// Redis stream. A click reaches the stream within about 20 ms of its redirect, so a crashed
// instance loses at most that much. Consumers on every instance write entries to PostgreSQL and
// delete them only after the write commits. Entries left pending by a silent consumer are taken
// over by another instance; event IDs make a second write of the same click a no-op.
package clickstream

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mathrand "math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

const (
	streamKey = "sh:clicks"
	deadKey   = "sh:clicks:dead"
	group     = "writers"

	flushEvery    = 20 * time.Millisecond
	redisTimeout  = time.Second
	readCount     = 4
	readBlock     = time.Second
	checkEvery    = time.Second
	claimEvery    = 30 * time.Second
	forgetIdle    = time.Hour
	deadMaxLen    = 10_000
	wireVersion   = "1"
	logFailuresAt = 10 * time.Second
)

// Fallback takes the clicks the stream can't: while Redis fails or the stream is over budget.
type Fallback interface {
	Record(click analytics.Click)
}

type Config struct {
	Buffer    int   // clicks waiting to be appended; more are dropped. Default 100000.
	BatchSize int   // most clicks per stream entry. Default 1000.
	Consumers int   // goroutines writing the stream to PostgreSQL. Default 2.
	MaxBytes  int64 // stream memory budget; above it new clicks go to the fallback. Default 64 MiB.
	// ClaimIdle is how long an entry may sit with a consumer that isn't working on it before another
	// consumer takes it over. Consumers retrying a write keep their entries fresh. Default 1 minute.
	ClaimIdle    time.Duration
	WriteTimeout time.Duration // deadline for each database attempt. Default 10s.
	RetryMin     time.Duration // initial retry delay, with jitter. Default 100ms.
	RetryMax     time.Duration // maximum retry delay. Default 5s.
}

// Stats are totals since the queue started, except the backlog, which is from the last check.
type Stats struct {
	Appended       uint64 // clicks appended to the stream
	Fallback       uint64 // clicks handed to the fallback
	Dropped        uint64 // clicks dropped because the buffer was full
	Written        uint64 // clicks this instance wrote to PostgreSQL
	Duplicates     uint64 // written clicks that were already stored
	FailedWrites   uint64
	Claimed        uint64 // entries taken over from other consumers
	DeadLetters    uint64 // entries moved to sh:clicks:dead
	BacklogEntries int64  // entries in the stream: not yet written, or being written
	BacklogBytes   int64  // estimated stream memory
}

type Queue struct {
	client     *redis.Client
	store      analytics.ClickStore
	fallback   Fallback
	cfg        Config
	consumer   string
	stream     string
	dead       string
	claimEvery time.Duration
	checkEvery time.Duration

	events       chan analytics.Click
	quit         chan struct{}
	producerDone chan struct{}
	closeOnce    sync.Once
	workers      sync.WaitGroup
	// Reads stop first at shutdown; writes already in progress get until the deadline.
	readCtx     context.Context
	stopReading context.CancelFunc
	writeCtx    context.Context
	stopWriting context.CancelFunc

	overBudget   atomic.Bool
	appendFailed atomic.Bool
	appendRetry  atomic.Int64 // unix nanos; appends skip Redis until then after a failure
	readLogged   atomic.Int64

	appended, fallbackCount, dropped, written, duplicates atomic.Uint64
	failedWrites, claimed, deadLetters                    atomic.Uint64
	backlogEntries, backlogBytes                          atomic.Int64
}

// New connects to Redis at url (redis:// or rediss://) and starts the producer and consumers.
// Redis may be down: clicks go to the fallback until it answers.
func New(url string, store analytics.ClickStore, fallback Fallback, cfg Config) (*Queue, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	opts.ContextTimeoutEnabled = true
	q := newQueue(redis.NewClient(opts), store, fallback, cfg)
	q.startProducer()
	q.startConsumers()
	return q, nil
}

func newQueue(client *redis.Client, store analytics.ClickStore, fallback Fallback, cfg Config) *Queue {
	if cfg.Buffer <= 0 {
		cfg.Buffer = 100_000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1000
	}
	if cfg.Consumers <= 0 {
		cfg.Consumers = 2
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 64 << 20
	}
	if cfg.ClaimIdle <= 0 {
		cfg.ClaimIdle = time.Minute
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.RetryMin <= 0 {
		cfg.RetryMin = 100 * time.Millisecond
	}
	if cfg.RetryMax < cfg.RetryMin {
		cfg.RetryMax = max(cfg.RetryMin, 5*time.Second)
	}
	host, _ := os.Hostname()
	q := &Queue{
		client:       client,
		store:        store,
		fallback:     fallback,
		cfg:          cfg,
		consumer:     host + "-" + strings.ToLower(rand.Text()[:8]),
		stream:       streamKey,
		dead:         deadKey,
		claimEvery:   claimEvery,
		checkEvery:   checkEvery,
		events:       make(chan analytics.Click, cfg.Buffer),
		quit:         make(chan struct{}),
		producerDone: make(chan struct{}),
	}
	q.readCtx, q.stopReading = context.WithCancel(context.Background())
	q.writeCtx, q.stopWriting = context.WithCancel(context.Background())
	return q
}

// Record queues a click without blocking. A full buffer drops it.
func (q *Queue) Record(click analytics.Click) {
	if click.CreatedAt.IsZero() {
		click.CreatedAt = time.Now()
	}
	click.CreatedAt = click.CreatedAt.Truncate(time.Microsecond)
	if click.EventID == "" {
		click.EventID = analytics.NewEventID()
	}
	select {
	case <-q.quit:
		q.toFallback([]analytics.Click{click})
		return
	default:
	}
	select {
	case q.events <- click:
	default:
		q.dropped.Add(1)
	}
}

// QueueDepth is the number of clicks waiting in memory to be appended.
func (q *Queue) QueueDepth() int {
	return len(q.events)
}

func (q *Queue) Stats() Stats {
	return Stats{
		Appended:       q.appended.Load(),
		Fallback:       q.fallbackCount.Load(),
		Dropped:        q.dropped.Load(),
		Written:        q.written.Load(),
		Duplicates:     q.duplicates.Load(),
		FailedWrites:   q.failedWrites.Load(),
		Claimed:        q.claimed.Load(),
		DeadLetters:    q.deadLetters.Load(),
		BacklogEntries: q.backlogEntries.Load(),
		BacklogBytes:   q.backlogBytes.Load(),
	}
}

// Close appends what is buffered, then lets consumers finish the entries they hold. At the ctx
// deadline, writes in progress are abandoned; their entries stay in the stream for another
// instance, or this one after a restart.
func (q *Queue) Close(ctx context.Context) error {
	q.closeOnce.Do(func() { close(q.quit) })
	var err error
	select {
	case <-q.producerDone:
	case <-ctx.Done():
		err = ctx.Err()
	}
	q.stopReading()
	stopped := make(chan struct{})
	go func() {
		q.workers.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		err = ctx.Err()
		q.stopWriting()
		select {
		case <-stopped:
		case <-time.After(readBlock + redisTimeout):
		}
	}
	q.stopWriting()
	q.client.Close()
	return err
}

func (q *Queue) startProducer() {
	go q.produce()
}

func (q *Queue) startConsumers() {
	q.createGroup(q.readCtx)
	for range q.cfg.Consumers {
		q.workers.Go(q.consume)
	}
	q.workers.Go(q.claimLoop)
	q.workers.Go(q.checkLoop)
}

func (q *Queue) produce() {
	defer close(q.producerDone)
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	batch := make([]analytics.Click, 0, q.cfg.BatchSize)
	flush := func() {
		if len(batch) > 0 {
			q.append(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case click := <-q.events:
			batch = append(batch, click)
			if len(batch) >= q.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-q.quit:
			for {
				select {
				case click := <-q.events:
					batch = append(batch, click)
					if len(batch) >= q.cfg.BatchSize {
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

// append adds batch to the stream as one entry, or hands it to the fallback. It doesn't keep batch.
func (q *Queue) append(batch []analytics.Click) {
	if q.overBudget.Load() || time.Now().UnixNano() < q.appendRetry.Load() {
		q.toFallback(batch)
		return
	}
	payload, err := encode(batch)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
		err = q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.stream, Values: []any{"v", wireVersion, "d", payload}}).Err()
		cancel()
	}
	if err != nil {
		// An append that timed out may still have landed; the event IDs make the fallback's
		// copy of those clicks a duplicate that is skipped.
		q.appendRetry.Store(time.Now().Add(time.Second).UnixNano())
		if !q.appendFailed.Swap(true) {
			log.Printf("click stream: append failed, writing clicks straight to PostgreSQL until Redis recovers: %v", err)
		}
		q.toFallback(batch)
		return
	}
	if q.appendFailed.Swap(false) {
		log.Println("click stream: appending to Redis again")
	}
	q.appended.Add(uint64(len(batch)))
}

func (q *Queue) toFallback(batch []analytics.Click) {
	for _, click := range batch {
		q.fallback.Record(click)
	}
	q.fallbackCount.Add(uint64(len(batch)))
}

func (q *Queue) createGroup(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	err := q.client.XGroupCreateMkStream(ctx, q.stream, group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") && q.readCtx.Err() == nil {
		log.Printf("click stream: create consumer group: %v", err)
	}
}

func (q *Queue) consume() {
	for q.readCtx.Err() == nil {
		streams, err := q.client.XReadGroup(q.readCtx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: q.consumer,
			Streams:  []string{q.stream, ">"},
			Count:    readCount,
			Block:    readBlock,
		}).Result()
		switch {
		case err == nil:
			for _, s := range streams {
				q.process(s.Messages)
			}
		case errors.Is(err, redis.Nil) || q.readCtx.Err() != nil:
		default:
			if strings.HasPrefix(err.Error(), "NOGROUP") {
				q.createGroup(q.readCtx)
			} else if q.shouldLog(&q.readLogged) {
				log.Printf("click stream: read: %v", err)
			}
			q.sleep(q.readCtx, time.Second)
		}
	}
}

// claimLoop takes over entries that silent consumers, such as crashed instances, left pending,
// and removes consumers that have been idle for an hour with nothing pending.
func (q *Queue) claimLoop() {
	for {
		start := "0-0"
		for q.readCtx.Err() == nil {
			ctx, cancel := context.WithTimeout(q.readCtx, redisTimeout)
			msgs, next, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: q.stream, Group: group, Consumer: q.consumer,
				MinIdle: q.cfg.ClaimIdle, Start: start, Count: readCount,
			}).Result()
			cancel()
			if err != nil {
				break
			}
			if len(msgs) > 0 {
				q.claimed.Add(uint64(len(msgs)))
				log.Printf("click stream: took over %d entries from a silent consumer", len(msgs))
				q.process(msgs)
			}
			if next == "0-0" || next == "" {
				break
			}
			start = next
		}
		q.forgetIdleConsumers()
		if !q.sleep(q.readCtx, q.claimEvery) {
			return
		}
	}
}

func (q *Queue) forgetIdleConsumers() {
	ctx, cancel := context.WithTimeout(q.readCtx, redisTimeout)
	defer cancel()
	consumers, err := q.client.XInfoConsumers(ctx, q.stream, group).Result()
	if err != nil {
		return
	}
	for _, c := range consumers {
		if c.Name != q.consumer && c.Pending == 0 && c.Idle > forgetIdle {
			q.client.XGroupDelConsumer(ctx, q.stream, group, c.Name)
		}
	}
}

// checkLoop measures the backlog; above the budget, new clicks bypass the stream.
func (q *Queue) checkLoop() {
	loggedMemoryError := false
	for {
		ctx, cancel := context.WithTimeout(q.readCtx, redisTimeout)
		pipe := q.client.Pipeline()
		length := pipe.XLen(ctx, q.stream)
		memory := pipe.MemoryUsage(ctx, q.stream)
		pipe.Exec(ctx)
		cancel()
		if n, err := length.Result(); err == nil {
			q.backlogEntries.Store(n)
		}
		bytes, err := memory.Result()
		switch {
		case errors.Is(err, redis.Nil):
			bytes, err = 0, nil
		case err != nil && !loggedMemoryError && q.readCtx.Err() == nil && !isConnectionError(err):
			loggedMemoryError = true
			log.Printf("click stream: MEMORY USAGE failed, so the backlog budget is not enforced: %v", err)
		}
		if err == nil {
			q.backlogBytes.Store(bytes)
			over := bytes > q.cfg.MaxBytes
			if q.overBudget.Swap(over) != over {
				if over {
					log.Printf("click stream: backlog is %d MiB, over its %d MiB budget; new clicks go straight to PostgreSQL",
						bytes>>20, q.cfg.MaxBytes>>20)
				} else {
					log.Println("click stream: backlog back under budget")
				}
			}
		}
		if !q.sleep(q.readCtx, q.checkEvery) {
			return
		}
	}
}

type entry struct {
	message redis.XMessage
	clicks  []analytics.Click
}

// process writes msgs to PostgreSQL and removes them from the stream. An entry PostgreSQL rejects
// as invalid goes to the dead-letter stream, so it can't hold up the rest.
func (q *Queue) process(msgs []redis.XMessage) {
	entries := make([]entry, 0, len(msgs))
	for _, m := range msgs {
		clicks, err := decode(m.Values)
		if err != nil {
			q.deadLetter(m, err)
			continue
		}
		entries = append(entries, entry{message: m, clicks: clicks})
	}
	if len(entries) == 0 {
		return
	}
	err := q.write(entries)
	if !isDataError(err) {
		return
	}
	if len(entries) == 1 {
		q.deadLetter(entries[0].message, err)
		return
	}
	for _, e := range entries {
		if err := q.write([]entry{e}); isDataError(err) {
			q.deadLetter(e.message, err)
		}
	}
}

// write retries until the entries are stored and acknowledged, PostgreSQL rejects them as
// invalid, or the queue shuts down.
func (q *Queue) write(entries []entry) error {
	var clicks []analytics.Click
	ids := make([]string, len(entries))
	for i, e := range entries {
		clicks = append(clicks, e.clicks...)
		ids[i] = e.message.ID
	}
	params := analytics.BatchParams(clicks)
	delay := q.cfg.RetryMin
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(q.writeCtx, q.cfg.WriteTimeout)
		inserted, err := q.store.RecordClicks(ctx, params)
		cancel()
		if err == nil {
			q.written.Add(uint64(len(clicks)))
			q.duplicates.Add(uint64(len(clicks) - int(inserted)))
			if attempt > 1 {
				log.Printf("click stream: wrote %d clicks after %d attempts", len(clicks), attempt)
			}
			q.ack(ids)
			return nil
		}
		if isDataError(err) {
			return err
		}
		q.failedWrites.Add(1)
		if q.writeCtx.Err() != nil {
			return q.writeCtx.Err()
		}
		wait := delay/2 + time.Duration(mathrand.Int64N(int64(delay-delay/2)))
		log.Printf("click stream: write of %d clicks failed (attempt %d); retrying in %s: %v", len(clicks), attempt, wait, err)
		q.touch(ids)
		if !q.sleep(q.writeCtx, wait) {
			return q.writeCtx.Err()
		}
		delay = min(delay*2, q.cfg.RetryMax)
	}
}

// touch resets the entries' idle time, so no other consumer takes them over while this one retries.
func (q *Queue) touch(ids []string) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()
	q.client.XClaimJustID(ctx, &redis.XClaimArgs{Stream: q.stream, Group: group, Consumer: q.consumer, Messages: ids})
}

func (q *Queue) ack(ids []string) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()
	_, err := q.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.XAck(ctx, q.stream, group, ids...)
		p.XDel(ctx, q.stream, ids...)
		return nil
	})
	if err != nil {
		log.Printf("click stream: acknowledge %d entries: %v; they will be written again and skipped", len(ids), err)
	}
}

func (q *Queue) deadLetter(m redis.XMessage, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()
	payload, _ := m.Values["d"].(string)
	err := q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: q.dead, MaxLen: deadMaxLen, Approx: true,
		Values: []any{"id", m.ID, "error", cause.Error(), "d", payload},
	}).Err()
	if err != nil {
		log.Printf("click stream: move entry %s to %s: %v", m.ID, q.dead, err)
		return
	}
	q.deadLetters.Add(1)
	log.Printf("click stream: moved entry %s to %s: %v", m.ID, q.dead, cause)
	q.ack([]string{m.ID})
}

func (q *Queue) sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (q *Queue) shouldLog(last *atomic.Int64) bool {
	now := time.Now().UnixNano()
	prev := last.Load()
	return now-prev >= int64(logFailuresAt) && last.CompareAndSwap(prev, now)
}

// isDataError reports whether PostgreSQL rejected the values themselves (SQLSTATE class 22 or
// 23), so retrying the same entry can never succeed.
func isDataError(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	class := string(pqErr.Code.Class())
	return class == "22" || class == "23"
}

func isConnectionError(err error) bool {
	var netErr interface{ Timeout() bool }
	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "connection refused")
}

type wireClick struct {
	EventID     string `json:"e"`
	At          int64  `json:"t"` // Unix microseconds
	LinkID      int64  `json:"l"`
	VisitorHash string `json:"v,omitempty"`
	RefererHost string `json:"r,omitempty"`
	Source      string `json:"s,omitempty"`
	UTMSource   string `json:"us,omitempty"`
	UTMMedium   string `json:"um,omitempty"`
	UTMCampaign string `json:"uc,omitempty"`
	DeviceType  string `json:"dt,omitempty"`
	Browser     string `json:"b,omitempty"`
	OS          string `json:"o,omitempty"`
	Country     string `json:"c,omitempty"`
	City        string `json:"ci,omitempty"`
	IsBot       bool   `json:"bot,omitempty"`
}

func encode(clicks []analytics.Click) (string, error) {
	wire := make([]wireClick, len(clicks))
	for i, c := range clicks {
		wire[i] = wireClick{
			EventID: c.EventID, At: c.CreatedAt.UnixMicro(), LinkID: c.LinkID, VisitorHash: c.VisitorHash,
			RefererHost: c.RefererHost, Source: c.Source, UTMSource: c.UTMSource, UTMMedium: c.UTMMedium,
			UTMCampaign: c.UTMCampaign, DeviceType: c.DeviceType, Browser: c.Browser, OS: c.OS,
			Country: c.Country, City: c.City, IsBot: c.IsBot,
		}
	}
	data, err := json.Marshal(wire)
	return string(data), err
}

func decode(values map[string]any) ([]analytics.Click, error) {
	if v, _ := values["v"].(string); v != wireVersion {
		return nil, fmt.Errorf("unknown entry version %q", v)
	}
	payload, _ := values["d"].(string)
	var wire []wireClick
	if err := json.Unmarshal([]byte(payload), &wire); err != nil {
		return nil, fmt.Errorf("decode entry: %w", err)
	}
	if len(wire) == 0 {
		return nil, errors.New("empty entry")
	}
	clicks := make([]analytics.Click, len(wire))
	for i, w := range wire {
		if !validUUID(w.EventID) || w.LinkID <= 0 || w.At <= 0 {
			return nil, fmt.Errorf("invalid click %d in entry", i)
		}
		clicks[i] = analytics.Click{
			EventID: w.EventID, CreatedAt: time.UnixMicro(w.At).UTC(), LinkID: w.LinkID, VisitorHash: w.VisitorHash,
			RefererHost: w.RefererHost, Source: w.Source, UTMSource: w.UTMSource, UTMMedium: w.UTMMedium,
			UTMCampaign: w.UTMCampaign, DeviceType: w.DeviceType, Browser: w.Browser, OS: w.OS,
			Country: w.Country, City: w.City, IsBot: w.IsBot,
		}
	}
	return clicks, nil
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case !('0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F'):
			return false
		}
	}
	return true
}
