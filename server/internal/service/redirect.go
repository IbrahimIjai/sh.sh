package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/cache"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

// RedirectTarget is the part of a link that redirects and previews need.
type RedirectTarget struct {
	ID        int64
	Slug      string
	TargetURL string
	Title     string
	CreatedAt time.Time
	ExpiresAt sql.NullTime
}

type RedirectConfig struct {
	// CacheSize 0 turns the cache off.
	CacheSize int
	CacheTTL  time.Duration
	Now       func() time.Time
}

type RedirectService struct {
	queries db.Querier
	cache   *cache.TTL[RedirectTarget]
	now     func() time.Time
}

func NewRedirectService(queries db.Querier, cfg RedirectConfig) *RedirectService {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &RedirectService{
		queries: queries,
		cache:   cache.New[RedirectTarget](cfg.CacheSize, cfg.CacheTTL, min(cfg.CacheTTL, 5*time.Second)),
		now:     now,
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

// Forget drops a slug from the cache after its link is created or deleted.
func (s *RedirectService) Forget(slug string) {
	s.cache.Forget(slug)
}

func (s *RedirectService) load(ctx context.Context, slug string) (RedirectTarget, bool, error) {
	link, err := s.queries.GetLinkBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return RedirectTarget{}, false, nil
	}
	if err != nil {
		return RedirectTarget{}, false, err
	}
	return RedirectTarget{
		ID:        link.ID,
		Slug:      link.Slug,
		TargetURL: link.TargetUrl,
		Title:     link.Title.String,
		CreatedAt: link.CreatedAt,
		ExpiresAt: link.ExpiresAt,
	}, true, nil
}
