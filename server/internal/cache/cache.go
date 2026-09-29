package cache

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const defaultLoadTimeout = 5 * time.Second

type LoadFunc[V any] func(ctx context.Context, key string) (V, bool, error)

// Stats are totals since the cache was created.
type Stats struct {
	Hits      uint64
	Misses    uint64
	Coalesced uint64 // misses that waited for another request's load instead of loading
	Stale     uint64 // expired entries served because the load failed
}

// TTL is a size-bounded LRU cache that also remembers misses. Concurrent misses for one key share
// a single load. A nil *TTL loads every time.
type TTL[V any] struct {
	mu          sync.Mutex
	capacity    int
	ttl         time.Duration
	missTTL     time.Duration
	staleFor    time.Duration
	loadTimeout time.Duration
	items       map[string]*list.Element
	order       *list.List
	loading     map[string]*flight[V]
	now         func() time.Time

	hits, misses, coalesced, stale atomic.Uint64
}

type entry[V any] struct {
	key     string
	value   V
	found   bool
	expires time.Time
}

// flight is one load shared by every request that missed the same key meanwhile. Forget detaches
// it, so a load that read the database before a delete never stores what it read.
type flight[V any] struct {
	done      chan struct{}
	value     V
	found     bool
	err       error
	forgotten bool
}

func New[V any](capacity int, ttl, missTTL time.Duration) *TTL[V] {
	if capacity <= 0 || ttl <= 0 {
		return nil
	}
	return &TTL[V]{
		capacity:    capacity,
		ttl:         ttl,
		missTTL:     missTTL,
		loadTimeout: defaultLoadTimeout,
		items:       make(map[string]*list.Element),
		order:       list.New(),
		loading:     make(map[string]*flight[V]),
		now:         time.Now,
	}
}

// WithStale lets Get return an entry up to d past its expiry when reloading it fails.
func (c *TTL[V]) WithStale(d time.Duration) *TTL[V] {
	if c != nil {
		c.staleFor = d
	}
	return c
}

func (c *TTL[V]) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	return Stats{Hits: c.hits.Load(), Misses: c.misses.Load(), Coalesced: c.coalesced.Load(), Stale: c.stale.Load()}
}

func (c *TTL[V]) Get(ctx context.Context, key string, load LoadFunc[V]) (V, bool, error) {
	if c == nil {
		return load(ctx, key)
	}

	c.mu.Lock()
	if e, fresh := c.lookup(key); fresh {
		c.mu.Unlock()
		c.hits.Add(1)
		return e.value, e.found, nil
	}
	c.misses.Add(1)
	if f, ok := c.loading[key]; ok {
		c.mu.Unlock()
		c.coalesced.Add(1)
		select {
		case <-f.done:
			return f.value, f.found, f.err
		case <-ctx.Done():
			var zero V
			return zero, false, ctx.Err()
		}
	}
	f := &flight[V]{done: make(chan struct{})}
	c.loading[key] = f
	c.mu.Unlock()

	// One caller's disconnect must not fail the others waiting on this load.
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.loadTimeout)
	value, found, err := load(loadCtx, key)
	cancel()

	c.mu.Lock()
	if c.loading[key] == f {
		delete(c.loading, key)
	}
	switch {
	case err == nil && !f.forgotten:
		c.store(key, value, found)
	case err != nil:
		if v, ok, staleOK := c.staleEntry(key); staleOK {
			value, found, err = v, ok, nil
			c.stale.Add(1)
		}
	}
	f.value, f.found, f.err = value, found, err
	c.mu.Unlock()
	close(f.done)
	return value, found, err
}

func (c *TTL[V]) Forget(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.Remove(el)
		delete(c.items, key)
	}
	c.detach(key)
}

// Set stores a value known to be current, replacing any entry, including a remembered miss.
func (c *TTL[V]) Set(key string, value V) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.detach(key)
	c.store(key, value, true)
}

// detach must be called with c.mu held. The detached load still answers its waiters but never
// stores its result.
func (c *TTL[V]) detach(key string) {
	if f, ok := c.loading[key]; ok {
		f.forgotten = true
		delete(c.loading, key)
	}
}

// lookup must be called with c.mu held. Expired entries stay until they're past the stale window,
// so a failed reload can still fall back to them.
func (c *TTL[V]) lookup(key string) (entry[V], bool) {
	el, ok := c.items[key]
	if !ok {
		return entry[V]{}, false
	}
	e := el.Value.(*entry[V])
	now := c.now()
	if now.Before(e.expires) {
		c.order.MoveToFront(el)
		return *e, true
	}
	if !now.Before(e.expires.Add(c.staleFor)) {
		c.order.Remove(el)
		delete(c.items, key)
	}
	return entry[V]{}, false
}

// staleEntry must be called with c.mu held.
func (c *TTL[V]) staleEntry(key string) (V, bool, bool) {
	var zero V
	el, ok := c.items[key]
	if !ok || c.staleFor <= 0 {
		return zero, false, false
	}
	e := el.Value.(*entry[V])
	if !c.now().Before(e.expires.Add(c.staleFor)) {
		return zero, false, false
	}
	return e.value, e.found, true
}

// store must be called with c.mu held.
func (c *TTL[V]) store(key string, value V, found bool) {
	ttl := c.ttl
	if !found {
		ttl = c.missTTL
	}
	if ttl <= 0 {
		return
	}
	e := &entry[V]{key: key, value: value, found: found, expires: c.now().Add(ttl)}

	if el, ok := c.items[key]; ok {
		el.Value = e
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(e)
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*entry[V]).key)
	}
}
