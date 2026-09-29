package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

type LinkHandler struct {
	links   *service.LinkService
	baseURL string
}

// NewLinkHandler serves the links API. baseURL is where short links live, e.g. https://shortened.cv.
func NewLinkHandler(links *service.LinkService, baseURL string) *LinkHandler {
	return &LinkHandler{links: links, baseURL: strings.TrimRight(baseURL, "/")}
}

func (h *LinkHandler) Create(c *gin.Context) {
	userID, signedIn := optionalUserID(c)

	var req createLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	created, err := h.links.Create(c.Request.Context(), service.CreateLinkInput{
		UserID:      userID,
		SignedIn:    signedIn,
		ClientIP:    c.ClientIP(),
		TargetURL:   req.TargetURL,
		Slug:        req.Slug,
		Title:       req.Title,
		Description: req.Description,
	})
	if err != nil {
		writeError(c, err, "could not create link")
		return
	}

	resp := newLinkResponse(h.baseURL, created.Link, 0)
	resp.ClaimToken = created.ClaimToken
	c.JSON(http.StatusCreated, resp)
}

func (h *LinkHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	links, err := h.links.List(c.Request.Context(), userID)
	if err != nil {
		writeError(c, err, "could not list links")
		return
	}

	c.JSON(http.StatusOK, newLinkResponses(h.baseURL, links))
}

func (h *LinkHandler) Delete(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	if err := h.links.Delete(c.Request.Context(), userID, id); err != nil {
		writeError(c, err, "could not delete link")
		return
	}

	c.Status(http.StatusNoContent)
}

// Lookup returns the anonymous links behind the claim tokens a browser has saved, so its
// "recent links" list can show current click counts.
func (h *LinkHandler) Lookup(c *gin.Context) {
	var req claimTokensRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	links, err := h.links.Lookup(c.Request.Context(), req.ClaimTokens)
	if err != nil {
		writeError(c, err, "could not load links")
		return
	}

	c.JSON(http.StatusOK, newLinkResponses(h.baseURL, links))
}

// Claim moves anonymous links into the signed-in user's account. The browser should forget the
// claimed tokens afterwards.
func (h *LinkHandler) Claim(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	var req claimTokensRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	links, err := h.links.Claim(c.Request.Context(), userID, req.ClaimTokens)
	if err != nil {
		writeError(c, err, "could not claim links")
		return
	}

	c.JSON(http.StatusOK, gin.H{"claimed": newLinkResponses(h.baseURL, links)})
}
