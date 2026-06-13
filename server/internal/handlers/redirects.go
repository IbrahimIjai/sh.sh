package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

func (h *LinkHandler) Redirect(c *gin.Context) {
	slug, ok := redirectSlug(c)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	link, err := h.queries.GetLinkBySlug(c.Request.Context(), slug)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load link"})
		return
	}

	_, _ = h.queries.RecordClick(c.Request.Context(), db.RecordClickParams{
		LinkID:    link.ID,
		IpAddress: nullString(c.ClientIP()),
		UserAgent: nullString(c.Request.UserAgent()),
		Referer:   nullString(c.Request.Referer()),
	})
	_ = h.queries.IncrementLinkClickCount(c.Request.Context(), link.ID)

	c.Redirect(http.StatusFound, link.TargetUrl)
}

func redirectSlug(c *gin.Context) (string, bool) {
	if c.Request.Method != http.MethodGet {
		return "", false
	}

	slug := strings.Trim(c.Request.URL.Path, "/")
	return slug, slug != "" && !strings.Contains(slug, "/")
}
