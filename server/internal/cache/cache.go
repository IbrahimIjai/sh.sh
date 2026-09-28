package cache

import (
	"container/list"
	"context"
	"sync"
	"time"
)

type LoadFunc[V any] func(ctx context.Context, key string) (V, bool, error)

// TTL is a size-bounded LRU cache that also remembers misses. A nil *TTL loads every time.
type TTL[V any] struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	missTTL  time.Duration
	items    map[string]*list.Element
	order    *list.List
	loading  map[string]*inflight
	now      func() time.Time
}

// inflight counts the loads running for one key. Forget bumps gen, so a load that read the
// database before a delete can't store what it read after the key was forgotten.
type inflight struct {
	loads int
	gen   uint64
}

type entry[V any] struct {
	key     string
	value   V
	found   bool
	expires time.Time
}

func New[V any](capacity int, ttl, missTTL time.Duration) *TTL[V] {
	if capacity <= 0 || ttl <= 0 {
		return nil
	}
	return &TTL[V]{
		capacity: capacity,
		ttl:      ttl,
		missTTL:  missTTL,
		items:    make(map[string]*list.Element),
		order:    list.New(),
		loading:  make(map[string]*inflight),
		now:      time.Now,
	}
}

func (c *TTL[V]) Get(ctx context.Context, key string, load LoadFunc[V]) (V, bool, error) {
	if c == nil {
		return load(ctx, key)
	}
	e, hit, gen := c.lookup(key)
	if hit {
		return e.value, e.found, nil
	}
	value, found, err := load(ctx, key)
	c.finish(key, gen, value, found, err == nil)
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
	if l, ok := c.loading[key]; ok {
		l.gen++
	}
}

// lookup returns a fresh entry, or registers a load for key and returns its generation.
func (c *TTL[V]) lookup(key string) (entry[V], bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry[V])
		if c.now().Before(e.expires) {
			c.order.MoveToFront(el)
			return *e, true, 0
		}
		c.order.Remove(el)
		delete(c.items, key)
	}
	l, ok := c.loading[key]
	if !ok {
		l = &inflight{}
		c.loading[key] = l
	}
	l.loads++
	return entry[V]{}, false, l.gen
}

// finish ends a load and stores its result, unless it failed or the key was forgotten meanwhile.
func (c *TTL[V]) finish(key string, gen uint64, value V, found, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.loading[key]
	l.loads--
	if l.loads == 0 {
		delete(c.loading, key)
	}
	if ok && l.gen == gen {
		c.store(key, value, found)
	}
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
