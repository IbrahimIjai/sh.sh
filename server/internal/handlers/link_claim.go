package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

type claimTokensRequest struct {
	ClaimTokens []string `json:"claim_tokens" binding:"required,min=1,max=100"`
}

// Lookup returns the anonymous links behind the claim tokens a browser has saved, so its
// "recent links" list can show current click counts. No sign-in: the token is the credential.
func (h *LinkHandler) Lookup(c *gin.Context) {
	var req claimTokensRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	links, err := h.queries.ListLinksByClaimTokens(c.Request.Context(), hashClaimTokens(req.ClaimTokens))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load links"})
		return
	}

	c.JSON(http.StatusOK, h.linkResponses(links))
}

// Claim moves anonymous links into the signed-in user's account, clicks included. Claimed
// tokens stop working, so the browser should forget them afterwards.
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

	links, err := h.queries.ClaimLinks(c.Request.Context(), db.ClaimLinksParams{
		UserID:           sql.NullInt64{Int64: userID, Valid: true},
		ClaimTokenHashes: hashClaimTokens(req.ClaimTokens),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not claim links"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"claimed": h.linkResponses(links)})
}

func newClaimToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate claim token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// Claim tokens are 256-bit random values, so a plain SHA-256 is enough to keep them useless if
// the database leaks.
func hashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashClaimTokens(tokens []string) []string {
	hashes := make([]string, len(tokens))
	for i, token := range tokens {
		hashes[i] = hashClaimToken(token)
	}
	return hashes
}
