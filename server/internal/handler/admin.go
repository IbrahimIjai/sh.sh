package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

type AdminHandler struct {
	links *service.LinkService
}

func NewAdminHandler(links *service.LinkService) *AdminHandler {
	return &AdminHandler{links: links}
}

type takedownRequest struct {
	Slugs      []string `json:"slugs" binding:"max=1000"`
	TargetHost string   `json:"target_host"`
}

// Takedown deactivates abusive links by slug or by destination host (subdomains included) and
// stops them redirecting on every instance.
func (h *AdminHandler) Takedown(c *gin.Context) {
	var req takedownRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	slugs, err := h.links.Takedown(c.Request.Context(), req.Slugs, req.TargetHost)
	if err != nil {
		writeError(c, err, "could not take links down")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deactivated": slugs})
}
