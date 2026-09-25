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
	now      func() time.Time
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
		now:      time.Now,
	}
}

func (c *TTL[V]) Get(ctx context.Context, key string, load LoadFunc[V]) (V, bool, error) {
	if c == nil {
		return load(ctx, key)
	}
	if e, ok := c.lookup(key); ok {
		return e.value, e.found, nil
	}
	value, found, err := load(ctx, key)
	if err == nil {
		c.store(key, value, found)
	}
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
}

func (c *TTL[V]) lookup(key string) (entry[V], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return entry[V]{}, false
	}
	e := el.Value.(*entry[V])
	if !c.now().Before(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		return entry[V]{}, false
	}
	c.order.MoveToFront(el)
	return *e, true
}

func (c *TTL[V]) store(key string, value V, found bool) {
	ttl := c.ttl
	if !found {
		ttl = c.missTTL
	}
	if ttl <= 0 {
		return
	}
	e := &entry[V]{key: key, value: value, found: found, expires: c.now().Add(ttl)}

	c.mu.Lock()
	defer c.mu.Unlock()
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
