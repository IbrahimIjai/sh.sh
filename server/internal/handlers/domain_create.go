package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type createDomainRequest struct {
	Domain string `json:"domain" binding:"required"`
}

func (h *DomainHandler) Create(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	var req createDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	host, err := normalizeHost(req.Domain)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, err := randomSlug(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create verification token"})
		return
	}

	domain, err := h.queries.CreateDomain(c.Request.Context(), db.CreateDomainParams{
		UserID:            userID,
		Host:              host,
		VerificationToken: token,
	})
	if err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "domain already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create domain"})
		return
	}

	c.JSON(http.StatusCreated, domain)
}
