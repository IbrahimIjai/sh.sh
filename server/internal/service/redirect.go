package service

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/cache"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const (
	sharedReadTimeout = 50 * time.Millisecond
	// Fills and announcements are on the request path; a slow Redis only costs a cache entry.
	sharedFillTimeout   = 100 * time.Millisecond
	sharedRevokeTimeout = 2 * time.Second
	// revokedFor outlives any copy Redis may still hold (5-minute TTL), so a revocation that
	// didn't reach Redis can't let this instance serve the old link again.
	revokedFor = 10 * time.Minute
	// After a failed Redis call, request paths skip Redis this long instead of each paying its timeout.
	sharedBackoff = time.Second
)

// RedirectTarget is the part of a link that redirects and previews need.
type RedirectTarget struct {
	ID        int64        `json:"id"`
	Slug      string       `json:"slug"`
	TargetURL string       `json:"target_url"`
	Title     string       `json:"title,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	ExpiresAt sql.NullTime `json:"expires_at"`
}

// SharedCache is the cache shared by every API instance (Redis). Errors mean it is unavailable,
// never that a link doesn't exist.
type SharedCache interface {
	// Get reports hit=false when the slug isn't cached. A revoked slug is a hit with found=false.
	Get(ctx context.Context, slug string) (target RedirectTarget, found, hit bool, err error)
	// Fill caches a found link unless the slug already holds a value or a revocation.
	Fill(ctx context.Context, slug string, target RedirectTarget) error
	// Revoke replaces the slug with a revocation and tells every instance to forget it.
	Revoke(ctx context.Context, slug string) error
	// Announce tells every instance to forget the slug, clearing cached "not found" entries.
	Announce(ctx context.Context, slug string) error
	// Subscribe calls forget for every slug announced or revoked by any instance, until ctx ends.
	Subscribe(ctx context.Context, forget func(slug string)) error
}

type RedirectConfig struct {
	// CacheSize 0 turns the in-process cache off.
	CacheSize int
	CacheTTL  time.Duration
	// StaleFor serves an expired cached link this long past its TTL while the database is failing.
	StaleFor time.Duration
	// MaxDBLoads caps concurrent database lookups caused by cache misses. 0 means no cap.
	MaxDBLoads int
	// Shared is optional; without it each instance only has its own cache.
	Shared SharedCache
	Now    func() time.Time
}

type RedirectStats struct {
	Local    cache.Stats
	L2Hits   uint64
	L2Misses uint64
	L2Errors uint64
	DBLoads  uint64
}

type RedirectService struct {
	queries db.Querier
	cache   *cache.TTL[RedirectTarget]
	shared  SharedCache
	dbSlots chan struct{}
	now     func() time.Time

	sharedDownUntil atomic.Int64 // unix nanos

	revokedMu sync.Mutex
	revoked   map[string]time.Time // slug → until; lookups skip Redis for these

	l2Hits, l2Misses, l2Errors, dbLoads atomic.Uint64
}

func NewRedirectService(queries db.Querier, cfg RedirectConfig) *RedirectService {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	s := &RedirectService{
		queries: queries,
		cache:   cache.New[RedirectTarget](cfg.CacheSize, cfg.CacheTTL, min(cfg.CacheTTL, 5*time.Second)).WithStale(cfg.StaleFor),
		shared:  cfg.Shared,
		now:     now,
		revoked: map[string]time.Time{},
	}
	if cfg.MaxDBLoads > 0 {
		s.dbSlots = make(chan struct{}, cfg.MaxDBLoads)
	}
	return s
}

// Start applies other instances' invalidations to this instance's cache until ctx ends.
func (s *RedirectService) Start(ctx context.Context) {
	if s.shared == nil {
		return
	}
	go func() {
		for ctx.Err() == nil {
			if err := s.shared.Subscribe(ctx, s.cache.Forget); err != nil && ctx.Err() == nil {
				log.Printf("redirect cache: invalidation subscription: %v", err)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}
	}()
}

func (s *RedirectService) Stats() RedirectStats {
	return RedirectStats{
		Local:    s.cache.Stats(),
		L2Hits:   s.l2Hits.Load(),
		L2Misses: s.l2Misses.Load(),
		L2Errors: s.l2Errors.Load(),
		DBLoads:  s.dbLoads.Load(),
	}
}

// Resolve returns the active link behind slug; found is false when there is none or it has expired.
func (s *RedirectService) Resolve(ctx context.Context, slug string) (RedirectTarget, bool, error) {
	link, found, err := s.cache.Get(ctx, slug, s.load)
	if err != nil {
		return RedirectTarget{}, false, err
	}
	// A cached entry can outlive the link's expiry.
	if found && link.ExpiresAt.Valid && !s.now().Before(link.ExpiresAt.Time) {
		return link, false, nil
	}
	return link, found, nil
}

// Prime caches a new link here and in the shared cache, and makes every other instance forget
// a remembered "not found" for its slug, so its first redirects don't wait on the database.
func (s *RedirectService) Prime(ctx context.Context, link db.Link) {
	target := redirectTarget(link)
	s.cache.Set(link.Slug, target)
	if !s.sharedUp() {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*sharedFillTimeout)
	defer cancel()
	if err := s.shared.Fill(ctx, link.Slug, target); err != nil {
		s.sharedFailed()
		return
	}
	if err := s.shared.Announce(ctx, link.Slug); err != nil {
		s.sharedFailed()
		log.Printf("redirect cache: announce %q: %v", link.Slug, err)
	}
}

// Revoke stops a deleted or taken-down slug from resolving on every instance. This instance
// stops at once. An error means Redis wasn't updated: other instances may keep serving the slug
// for up to their cache TTL plus Redis's, while a background retry keeps trying.
func (s *RedirectService) Revoke(ctx context.Context, slug string) error {
	s.revokedMu.Lock()
	now := time.Now()
	for k, until := range s.revoked {
		if now.After(until) {
			delete(s.revoked, k)
		}
	}
	s.revoked[slug] = now.Add(revokedFor)
	s.revokedMu.Unlock()
	s.cache.Forget(slug)
	if s.shared == nil {
		return nil
	}
	if err := s.revokeShared(ctx, slug); err != nil {
		log.Printf("redirect cache: revoke %q: %v; retrying in the background", slug, err)
		go s.retryRevoke(slug)
		return err
	}
	return nil
}

func (s *RedirectService) revokeShared(ctx context.Context, slug string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sharedRevokeTimeout)
	defer cancel()
	err := s.shared.Revoke(ctx, slug)
	if err != nil {
		s.l2Errors.Add(1)
	}
	return err
}

func (s *RedirectService) retryRevoke(slug string) {
	delay := time.Second
	for deadline := time.Now().Add(revokedFor); time.Now().Before(deadline); {
		time.Sleep(delay)
		if err := s.revokeShared(context.Background(), slug); err == nil {
			log.Printf("redirect cache: revoked %q after retrying", slug)
			return
		}
		delay = min(delay*2, 30*time.Second)
	}
	log.Printf("redirect cache: gave up revoking %q in Redis; its cached copy expires on its own", slug)
}

func (s *RedirectService) sharedUp() bool {
	return s.shared != nil && time.Now().UnixNano() >= s.sharedDownUntil.Load()
}

func (s *RedirectService) sharedFailed() {
	s.l2Errors.Add(1)
	s.sharedDownUntil.Store(time.Now().Add(sharedBackoff).UnixNano())
}

func (s *RedirectService) isRevoked(slug string) bool {
	s.revokedMu.Lock()
	defer s.revokedMu.Unlock()
	until, ok := s.revoked[slug]
	return ok && time.Now().Before(until)
}

func (s *RedirectService) load(ctx context.Context, slug string) (RedirectTarget, bool, error) {
	shared := s.shared
	if !s.sharedUp() || s.isRevoked(slug) {
		shared = nil
	}
	if shared != nil {
		readCtx, cancel := context.WithTimeout(ctx, sharedReadTimeout)
		target, found, hit, err := shared.Get(readCtx, slug)
		cancel()
		switch {
		case err != nil:
			s.sharedFailed()
			shared = nil
		case hit:
			s.l2Hits.Add(1)
			return target, found, nil
		default:
			s.l2Misses.Add(1)
		}
	}

	if s.dbSlots != nil {
		select {
		case s.dbSlots <- struct{}{}:
			defer func() { <-s.dbSlots }()
		case <-ctx.Done():
			return RedirectTarget{}, false, ctx.Err()
		}
	}
	s.dbLoads.Add(1)
	link, err := s.queries.GetLinkBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return RedirectTarget{}, false, nil
	}
	if err != nil {
		return RedirectTarget{}, false, err
	}
	target := redirectTarget(link)
	if shared != nil {
		fillCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sharedFillTimeout)
		if err := shared.Fill(fillCtx, slug, target); err != nil {
			s.sharedFailed()
		}
		cancel()
	}
	return target, true, nil
}

func redirectTarget(link db.Link) RedirectTarget {
	return RedirectTarget{
		ID:        link.ID,
		Slug:      link.Slug,
		TargetURL: link.TargetUrl,
		Title:     link.Title.String,
		CreatedAt: link.CreatedAt,
		ExpiresAt: link.ExpiresAt,
	}
}
