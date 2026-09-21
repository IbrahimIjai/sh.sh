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
	"github.com/ibrahimijai/sh.sh/internal/analytics"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type ClickRecorder interface {
	Record(click analytics.Click)
}

// URLChecker returns the threats known for a URL; none means it isn't flagged.
type URLChecker interface {
	Check(ctx context.Context, rawURL string) ([]string, error)
}

type LinkConfig struct {
	// BaseURL is where short links live, e.g. https://shortened.cv.
	BaseURL   string
	Hasher    *analytics.Hasher
	Collector *analytics.Collector
	Clicks    ClickRecorder
	// Safety is optional; without it new links aren't checked.
	Safety URLChecker
}

type LinkHandler struct {
	queries   db.Querier
	baseURL   string
	ownHost   string
	hasher    *analytics.Hasher
	collector *analytics.Collector
	clicks    ClickRecorder
	safety    URLChecker
	now       func() time.Time
}

func NewLinkHandler(queries db.Querier, cfg LinkConfig) *LinkHandler {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	ownHost := ""
	if parsed, err := url.Parse(baseURL); err == nil {
		ownHost = parsed.Host
	}

	return &LinkHandler{
		queries:   queries,
		baseURL:   baseURL,
		ownHost:   ownHost,
		hasher:    cfg.Hasher,
		collector: cfg.Collector,
		clicks:    cfg.Clicks,
		safety:    cfg.Safety,
		now:       time.Now,
	}
}

func (h *LinkHandler) shortURL(slug string) string {
	return h.baseURL + "/" + slug
}

// ownedLink loads a link the user owns. It writes a 404 when the link doesn't exist or belongs
// to someone else, so callers can't tell the two apart.
func (h *LinkHandler) ownedLink(c *gin.Context, userID, id int64) (db.Link, bool) {
	link, err := h.queries.GetLinkByID(c.Request.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load link"})
		return db.Link{}, false
	}
	if err != nil || !link.UserID.Valid || link.UserID.Int64 != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return db.Link{}, false
	}
	return link, true
}
