package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

func (h *DomainHandler) Delete(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	err := h.queries.SoftDeleteDomain(c.Request.Context(), db.SoftDeleteDomainParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete domain"})
		return
	}

	c.Status(http.StatusNoContent)
}
