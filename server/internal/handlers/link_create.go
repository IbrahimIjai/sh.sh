package handlers

import (
	"database/sql"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const (
	maxTargetURLLength    = 2048
	anonymousLinksPerHour = 20
)

// Pointing at another shortener hides the real destination, which is how phishing links slip
// past filters.
var shortenerHosts = map[string]bool{
	"bit.ly": true, "bitly.com": true, "tinyurl.com": true, "goo.gl": true, "ow.ly": true,
	"is.gd": true, "v.gd": true, "buff.ly": true, "rebrand.ly": true, "cutt.ly": true,
	"shorturl.at": true, "rb.gy": true, "tiny.cc": true, "t.ly": true, "bl.ink": true,
	"s.id": true, "adf.ly": true, "shorte.st": true,
}

type createLinkRequest struct {
	TargetURL   string `json:"target_url" binding:"required"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Create shortens a URL for anyone. Signed-in users own the link. Anonymous users get a claim
// token, returned only in this response, which their browser keeps to list the link later and
// to claim it after signing up.
func (h *LinkHandler) Create(c *gin.Context) {
	userID, signedIn := optionalUserID(c)

	var req createLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := h.targetError(req.TargetURL); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if req.Slug != "" && !signedIn {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "sign in to choose a custom slug"})
		return
	}
	if req.Slug != "" && !validSlug(req.Slug) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slug must be 3-64 letters, numbers, '-' or '_', and not a reserved word"})
		return
	}

	var creatorHash string
	if !signedIn {
		creatorHash = h.hasher.Creator(c.ClientIP())
		count, err := h.queries.CountLinksByCreatorSince(c.Request.Context(), db.CountLinksByCreatorSinceParams{
			CreatorHash: nullString(creatorHash),
			CreatedAt:   h.now().Add(-time.Hour),
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create link"})
			return
		}
		if count >= anonymousLinksPerHour {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many links from this network; sign in or try again later"})
			return
		}
	}

	if h.flagged(c, req.TargetURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target_url is flagged as unsafe"})
		return
	}

	slug, err := h.resolveSlug(c, req.Slug)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create slug"})
		return
	}

	params := db.CreateLinkParams{
		Slug:        slug,
		TargetUrl:   req.TargetURL,
		Title:       nullString(req.Title),
		Description: nullString(req.Description),
	}
	var claimToken string
	if signedIn {
		params.UserID = sql.NullInt64{Int64: userID, Valid: true}
	} else {
		claimToken, err = newClaimToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create link"})
			return
		}
		params.ClaimTokenHash = nullString(hashClaimToken(claimToken))
		params.CreatorHash = nullString(creatorHash)
	}

	link, err := h.queries.CreateLink(c.Request.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create link"})
		return
	}

	resp := h.linkResponse(link)
	resp.ClaimToken = claimToken
	c.JSON(http.StatusCreated, resp)
}

// targetError explains why a URL can't be shortened, or returns "" when it can.
func (h *LinkHandler) targetError(raw string) string {
	if len(raw) > maxTargetURLLength {
		return "target_url is too long"
	}
	if !validURL(raw) {
		return "target_url must be a valid http or https URL"
	}
	parsed, _ := url.Parse(raw)
	if strings.EqualFold(parsed.Host, h.ownHost) {
		return "target_url is already a short link"
	}
	if shortenerHosts[strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")] {
		return "target_url can't be another URL shortener's link"
	}
	return ""
}

// flagged reports whether Web Risk lists the URL. A failed check is logged and let through, so
// a Web Risk outage doesn't stop people from creating links.
func (h *LinkHandler) flagged(c *gin.Context, rawURL string) bool {
	if h.safety == nil {
		return false
	}
	threats, err := h.safety.Check(c.Request.Context(), rawURL)
	if err != nil {
		log.Printf("web risk check failed: %v", err)
		return false
	}
	return len(threats) > 0
}
