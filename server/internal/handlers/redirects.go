package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// UTM tags on a short link (shortened.cv/abc?utm_source=newsletter) are recorded with the click
// and passed on to the destination.
var forwardedParams = []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content"}

func (h *LinkHandler) Redirect(c *gin.Context) {
	slug, preview, ok := redirectSlug(c)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	link, found, err := h.redirects.Get(c.Request.Context(), slug, h.loadRedirect)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load link"})
		return
	}
	if found && link.ExpiresAt.Valid && !h.now().Before(link.ExpiresAt.Time) {
		found = false
	}

	if preview {
		h.renderPreview(c, slug, link, found)
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}

	h.clicks.Record(h.collector.Collect(c.Request, c.ClientIP(), link.ID))

	// 302, not 301: browsers cache a 301 and later clicks would never reach us to be counted.
	c.Redirect(http.StatusFound, withForwardedParams(link.TargetURL, c.Request.URL.Query()))
}

type redirectLink struct {
	ID        int64
	Slug      string
	TargetURL string
	Title     string
	CreatedAt time.Time
	ExpiresAt sql.NullTime
}

func (h *LinkHandler) loadRedirect(ctx context.Context, slug string) (redirectLink, bool, error) {
	link, err := h.queries.GetLinkBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return redirectLink{}, false, nil
	}
	if err != nil {
		return redirectLink{}, false, err
	}
	return redirectLink{
		ID:        link.ID,
		Slug:      link.Slug,
		TargetURL: link.TargetUrl,
		Title:     link.Title.String,
		CreatedAt: link.CreatedAt,
		ExpiresAt: link.ExpiresAt,
	}, true, nil
}

// redirectSlug reads the slug from GET /{slug}, or /{slug}+ for the preview page.
func redirectSlug(c *gin.Context) (slug string, preview bool, ok bool) {
	if c.Request.Method != http.MethodGet {
		return "", false, false
	}

	slug, preview = strings.CutSuffix(strings.Trim(c.Request.URL.Path, "/"), "+")
	return slug, preview, slug != "" && !strings.Contains(slug, "/")
}

// withForwardedParams appends the short link's UTM tags to the target URL, unless the target
// already sets them. The target's own query string is left byte-for-byte as it was.
func withForwardedParams(target string, query url.Values) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return target
	}

	existing := parsed.Query()
	extra := url.Values{}
	for _, key := range forwardedParams {
		if value := query.Get(key); value != "" && !existing.Has(key) {
			extra.Set(key, value)
		}
	}
	if len(extra) == 0 {
		return target
	}

	if parsed.RawQuery == "" {
		parsed.RawQuery = extra.Encode()
	} else {
		parsed.RawQuery += "&" + extra.Encode()
	}
	return parsed.String()
}
