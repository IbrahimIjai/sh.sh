package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *LinkHandler) Delete(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	link, err := h.queries.GetLinkByID(c.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || !link.UserID.Valid || link.UserID.Int64 != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load link"})
		return
	}

	if err := h.queries.SoftDeleteLink(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete link"})
		return
	}

	c.Status(http.StatusNoContent)
}
