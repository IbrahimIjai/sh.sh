package service

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/analytics"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const (
	maxTargetURLLength    = 2048
	anonymousLinksPerHour = 20
	userLinksPageSize     = 50
)

// Pointing at another shortener hides the real destination, which is how phishing links slip
// past filters.
var shortenerHosts = map[string]bool{
	"bit.ly": true, "bitly.com": true, "tinyurl.com": true, "goo.gl": true, "ow.ly": true,
	"is.gd": true, "v.gd": true, "buff.ly": true, "rebrand.ly": true, "cutt.ly": true,
	"shorturl.at": true, "rb.gy": true, "tiny.cc": true, "t.ly": true, "bl.ink": true,
	"s.id": true, "adf.ly": true, "shorte.st": true,
}

// URLChecker returns the threats known for a URL; none means it isn't flagged.
type URLChecker interface {
	Check(ctx context.Context, rawURL string) ([]string, error)
}

type LinkConfig struct {
	Hasher *analytics.Hasher
	// Safety is optional; without it new links aren't checked.
	Safety    URLChecker
	Redirects *RedirectService
	// OwnHost is the short-link host. Links pointing back at it are rejected.
	OwnHost string
	Now     func() time.Time
	NewSlug func() (string, error)
}

type LinkService struct {
	queries   db.Querier
	hasher    *analytics.Hasher
	safety    URLChecker
	redirects *RedirectService
	ownHost   string
	now       func() time.Time
	newSlug   func() (string, error)
}

func NewLinkService(queries db.Querier, cfg LinkConfig) *LinkService {
	s := &LinkService{
		queries:   queries,
		hasher:    cfg.Hasher,
		safety:    cfg.Safety,
		redirects: cfg.Redirects,
		ownHost:   cfg.OwnHost,
		now:       cfg.Now,
		newSlug:   cfg.NewSlug,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newSlug == nil {
		s.newSlug = func() (string, error) { return randomSlug(generatedSlugLength) }
	}
	return s
}

type CreateLinkInput struct {
	UserID      int64
	SignedIn    bool
	ClientIP    string
	TargetURL   string
	Slug        string
	Title       string
	Description string
}

type CreatedLink struct {
	Link db.Link
	// ClaimToken is set only for anonymous links, and is never stored.
	ClaimToken string
}

// Create shortens a URL for anyone. Signed-in users own the link. Anonymous users get a claim
// token, which their browser keeps to list the link later and to claim it after signing up.
func (s *LinkService) Create(ctx context.Context, in CreateLinkInput) (CreatedLink, error) {
	if msg := s.targetError(in.TargetURL); msg != "" {
		return CreatedLink{}, &InputError{Message: msg}
	}
	if in.Slug != "" && !in.SignedIn {
		return CreatedLink{}, ErrSignInRequired
	}
	if in.Slug != "" && !validSlug(in.Slug) {
		return CreatedLink{}, &InputError{Message: "slug must be 3-64 letters, numbers, '-' or '_', and not a reserved word"}
	}

	var creatorHash string
	if !in.SignedIn {
		creatorHash = s.hasher.Creator(in.ClientIP)
		count, err := s.queries.CountLinksByCreatorSince(ctx, db.CountLinksByCreatorSinceParams{
			CreatorHash: nullString(creatorHash),
			CreatedAt:   s.now().Add(-time.Hour),
		})
		if err != nil {
			return CreatedLink{}, err
		}
		if count >= anonymousLinksPerHour {
			return CreatedLink{}, ErrRateLimited
		}
	}

	if s.flagged(ctx, in.TargetURL) {
		return CreatedLink{}, ErrUnsafeURL
	}

	params := db.CreateLinkParams{
		Slug:        in.Slug,
		TargetUrl:   in.TargetURL,
		Title:       nullString(in.Title),
		Description: nullString(in.Description),
	}
	var claimToken string
	if in.SignedIn {
		params.UserID = sql.NullInt64{Int64: in.UserID, Valid: true}
	} else {
		var err error
		claimToken, err = newClaimToken()
		if err != nil {
			return CreatedLink{}, err
		}
		params.ClaimTokenHash = nullString(hashClaimToken(claimToken))
		params.CreatorHash = nullString(creatorHash)
	}

	link, err := s.insertLink(ctx, params)
	if err != nil {
		return CreatedLink{}, err
	}
	s.redirects.Forget(link.Slug)
	return CreatedLink{Link: link, ClaimToken: claimToken}, nil
}

func (s *LinkService) List(ctx context.Context, userID int64) ([]db.Link, error) {
	return s.queries.ListLinksByUser(ctx, db.ListLinksByUserParams{
		UserID: sql.NullInt64{Int64: userID, Valid: true},
		Limit:  userLinksPageSize,
	})
}

func (s *LinkService) Delete(ctx context.Context, userID, id int64) error {
	link, err := s.owned(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.queries.SoftDeleteLink(ctx, id); err != nil {
		return err
	}
	s.redirects.Forget(link.Slug)
	return nil
}

// Lookup returns the anonymous links behind the claim tokens a browser has saved. No sign-in:
// the token is the credential.
func (s *LinkService) Lookup(ctx context.Context, claimTokens []string) ([]db.Link, error) {
	return s.queries.ListLinksByClaimTokens(ctx, hashClaimTokens(claimTokens))
}

// Claim moves anonymous links into the user's account, clicks included. Claimed tokens stop working.
func (s *LinkService) Claim(ctx context.Context, userID int64, claimTokens []string) ([]db.Link, error) {
	return s.queries.ClaimLinks(ctx, db.ClaimLinksParams{
		UserID:           sql.NullInt64{Int64: userID, Valid: true},
		ClaimTokenHashes: hashClaimTokens(claimTokens),
	})
}

// owned loads a link the user owns. A missing link and someone else's link both return
// ErrLinkNotFound, so callers can't tell the two apart.
func (s *LinkService) owned(ctx context.Context, userID, id int64) (db.Link, error) {
	link, err := s.queries.GetLinkByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Link{}, ErrLinkNotFound
	}
	if err != nil {
		return db.Link{}, err
	}
	if !link.UserID.Valid || link.UserID.Int64 != userID {
		return db.Link{}, ErrLinkNotFound
	}
	return link, nil
}

// targetError explains why a URL can't be shortened, or returns "" when it can.
func (s *LinkService) targetError(raw string) string {
	if len(raw) > maxTargetURLLength {
		return "target_url is too long"
	}
	if !validURL(raw) {
		return "target_url must be a valid http or https URL"
	}
	parsed, _ := url.Parse(raw)
	if strings.EqualFold(parsed.Host, s.ownHost) {
		return "target_url is already a short link"
	}
	if shortenerHosts[strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")] {
		return "target_url can't be another URL shortener's link"
	}
	return ""
}

// flagged reports whether Web Risk lists the URL. A failed check is logged and let through, so
// a Web Risk outage doesn't stop people from creating links.
func (s *LinkService) flagged(ctx context.Context, rawURL string) bool {
	if s.safety == nil {
		return false
	}
	threats, err := s.safety.Check(ctx, rawURL)
	if err != nil {
		log.Printf("web risk check failed: %v", err)
		return false
	}
	return len(threats) > 0
}

func validURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
