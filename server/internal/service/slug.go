package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const (
	slugAlphabet          = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	generatedSlugLength   = 7
	generatedSlugAttempts = 5
)

// 256 isn't a multiple of 62, so b%62 over every byte would favour the first 8 characters.
const unbiasedByteLimit = 256 - 256%len(slugAlphabet)

var errSlugsExhausted = errors.New("no free random slug found")

// Custom slugs can't contain "/" or "+" (the redirect handler reads "/{slug}+" as a preview) and
// can't shadow a route, or the link would be saved but never reachable.
var customSlugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,128}$`)

const invalidSlugMessage = "slug must be 3-128 letters, numbers, '-' or '_', and not a reserved word"

var reservedSlugs = map[string]bool{
	"api": true, "auth": true, "health": true, "admin": true, "login": true, "logout": true,
	"signin": true, "signup": true, "settings": true, "dashboard": true, "static": true, "assets": true,
}

func validSlug(slug string) bool {
	return customSlugPattern.MatchString(slug) && !reservedSlugs[strings.ToLower(slug)]
}

func randomSlug(length int) (string, error) {
	return randomSlugFrom(rand.Reader, length)
}

func randomSlugFrom(r io.Reader, length int) (string, error) {
	slug := make([]byte, 0, length)
	buf := make([]byte, length)
	for len(slug) < length {
		n, err := r.Read(buf)
		for _, b := range buf[:n] {
			if int(b) < unbiasedByteLimit && len(slug) < length {
				slug = append(slug, slugAlphabet[int(b)%len(slugAlphabet)])
			}
		}
		if err != nil && len(slug) < length {
			return "", fmt.Errorf("generate slug: %w", err)
		}
	}
	return string(slug), nil
}

// insertLink inserts the link under params.Slug, or under a random slug when it is empty.
func (s *LinkService) insertLink(ctx context.Context, params db.CreateLinkParams) (db.Link, error) {
	if params.Slug != "" {
		params.Custom = true
		link, err := s.queries.CreateLink(ctx, params)
		if slugTaken(err) {
			return db.Link{}, ErrSlugTaken
		}
		return link, err
	}

	for range generatedSlugAttempts {
		slug, err := s.newSlug()
		if err != nil {
			return db.Link{}, err
		}
		params.Slug = slug
		link, err := s.queries.CreateLink(ctx, params)
		if !slugTaken(err) {
			return link, err
		}
	}
	return db.Link{}, errSlugsExhausted
}

// slugTaken reports a write that lost its slug: no row when the conflict was expected, a unique
// violation when another request claimed the slug at the same moment.
func slugTaken(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || isUniqueViolation(err)
}

func newClaimToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate claim token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// Claim tokens are 256-bit random values, so a plain SHA-256 is enough to keep them useless if
// the database leaks.
func hashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashClaimTokens(tokens []string) []string {
	hashes := make([]string, len(tokens))
	for i, token := range tokens {
		hashes[i] = hashClaimToken(token)
	}
	return hashes
}
