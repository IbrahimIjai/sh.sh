package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

type DomainHandler struct {
	domains *service.DomainService
}

func NewDomainHandler(domains *service.DomainService) *DomainHandler {
	return &DomainHandler{domains: domains}
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

	domain, err := h.domains.Create(c.Request.Context(), userID, req.Domain)
	if err != nil {
		writeError(c, err, "could not create domain")
		return
	}

	c.JSON(http.StatusCreated, domain)
}

func (h *DomainHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	domains, err := h.domains.List(c.Request.Context(), userID)
	if err != nil {
		writeError(c, err, "could not list domains")
		return
	}
	if domains == nil {
		domains = []db.Domain{}
	}

	c.JSON(http.StatusOK, domains)
}

func (h *DomainHandler) Delete(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	if err := h.domains.Delete(c.Request.Context(), userID, id); err != nil {
		writeError(c, err, "could not delete domain")
		return
	}

	c.Status(http.StatusNoContent)
}
