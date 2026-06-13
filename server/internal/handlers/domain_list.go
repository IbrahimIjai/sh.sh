package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

func (h *DomainHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	domains, err := h.queries.ListDomainsByUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list domains"})
		return
	}
	if domains == nil {
		domains = []db.Domain{}
	}

	c.JSON(http.StatusOK, domains)
}
