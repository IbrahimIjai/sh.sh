package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

func (h *LinkHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	links, err := h.queries.ListLinksByUser(c.Request.Context(), db.ListLinksByUserParams{
		UserID: sql.NullInt64{Int64: userID, Valid: true},
		Limit:  50,
		Offset: 0,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list links"})
		return
	}

	c.JSON(http.StatusOK, h.linkResponses(links))
}
