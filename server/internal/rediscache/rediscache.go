// Package rediscache is the Redis side of the redirect cache, cache invalidation across API
// instances, and the anonymous-creation rate limit. Every key it writes has a TTL, so Redis can
// run with maxmemory-policy volatile-lru.
package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	linkPrefix = "sh:link:"
	ratePrefix = "sh:rate:"
	channel    = "sh:invalidate"
	revoked    = "-"

	linkTTL = 5 * time.Minute
	// A revocation outlives any cached copy, so a fill that read the database just before the
	// delete can't bring the link back.
	revokedTTL = 10 * time.Minute
)

type Store struct {
	client *redis.Client
}

// Open parses url (redis:// or rediss://) and checks the connection. An unreachable server
// returns the store together with the ping error: the client keeps reconnecting, and callers
// fall back while Redis is down.
func Open(url string) (*Store, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	// Without this, go-redis ignores context deadlines on reads and writes, and a stalled Redis
	// stalls every redirect that misses the local cache.
	opts.ContextTimeoutEnabled = true
	store := &Store{client: redis.NewClient(opts)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.client.Ping(ctx).Err(); err != nil {
		return store, fmt.Errorf("ping redis: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.client.Close()
}

func (s *Store) Get(ctx context.Context, slug string) (service.RedirectTarget, bool, bool, error) {
	value, err := s.client.Get(ctx, linkPrefix+slug).Result()
	if errors.Is(err, redis.Nil) {
		return service.RedirectTarget{}, false, false, nil
	}
	if err != nil {
		return service.RedirectTarget{}, false, false, err
	}
	if value == revoked {
		return service.RedirectTarget{}, false, true, nil
	}
	var target service.RedirectTarget
	if err := json.Unmarshal([]byte(value), &target); err != nil {
		return service.RedirectTarget{}, false, false, err
	}
	return target, true, true, nil
}

func (s *Store) Fill(ctx context.Context, slug string, target service.RedirectTarget) error {
	value, err := json.Marshal(target)
	if err != nil {
		return err
	}
	return s.client.SetNX(ctx, linkPrefix+slug, value, linkTTL).Err()
}

func (s *Store) Revoke(ctx context.Context, slug string) error {
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, linkPrefix+slug, revoked, revokedTTL)
		pipe.Publish(ctx, channel, slug)
		return nil
	})
	return err
}

func (s *Store) Announce(ctx context.Context, slug string) error {
	return s.client.Publish(ctx, channel, slug).Err()
}

// Subscribe delivers invalidations until ctx ends. Pub/sub isn't durable: an instance that is
// disconnected misses messages and relies on its own cache TTL instead.
func (s *Store) Subscribe(ctx context.Context, forget func(slug string)) error {
	sub := s.client.Subscribe(ctx, channel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-messages:
			if !ok {
				return errors.New("subscription closed")
			}
			forget(msg.Payload)
		}
	}
}

// Allow counts one event for key and reports whether it is within limit for the current fixed
// window (windows are aligned to the Unix epoch, so an hourly window resets on the hour).
func (s *Store) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	bucket := time.Now().UnixNano() / int64(window)
	rateKey := ratePrefix + key + ":" + strconv.FormatInt(bucket, 10)
	var count *redis.IntCmd
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		count = pipe.Incr(ctx, rateKey)
		pipe.Expire(ctx, rateKey, window)
		return nil
	})
	if err != nil {
		return false, err
	}
	return count.Val() <= int64(limit), nil
}
