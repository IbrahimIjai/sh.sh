package handlers

import (
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

	if _, ok := h.ownedLink(c, userID, id); !ok {
		return
	}

	if err := h.queries.SoftDeleteLink(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete link"})
		return
	}

	c.Status(http.StatusNoContent)
}
