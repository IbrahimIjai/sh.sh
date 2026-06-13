package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type createLinkRequest struct {
	TargetURL   string `json:"target_url" binding:"required"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (h *LinkHandler) Create(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	var req createLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validURL(req.TargetURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target_url must be a valid http or https URL"})
		return
	}

	slug, err := h.resolveSlug(c, req.Slug)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create slug"})
		return
	}

	link, err := h.queries.CreateLink(c.Request.Context(), db.CreateLinkParams{
		UserID:      sql.NullInt64{Int64: userID, Valid: true},
		Slug:        slug,
		TargetUrl:   req.TargetURL,
		Title:       nullString(req.Title),
		Description: nullString(req.Description),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create link"})
		return
	}

	c.JSON(http.StatusCreated, link)
}
