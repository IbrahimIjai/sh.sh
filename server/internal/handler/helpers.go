package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/auth"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

func currentUserID(c *gin.Context) (int64, bool) {
	value, ok := c.Get(auth.UserIDKey)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return 0, false
	}

	userID, ok := value.(int64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session user"})
		return 0, false
	}

	return userID, true
}

// optionalUserID returns the signed-in user set by OptionalSession, if there is one.
func optionalUserID(c *gin.Context) (int64, bool) {
	value, ok := c.Get(auth.UserIDKey)
	if !ok {
		return 0, false
	}
	userID, ok := value.(int64)
	return userID, ok
}

func parseIDParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + name})
		return 0, false
	}
	return id, true
}

// writeError sends the status for a known service error, or 500 with fallback for anything else.
func writeError(c *gin.Context, err error, fallback string) {
	var input *service.InputError
	switch {
	case errors.As(err, &input):
		c.JSON(http.StatusBadRequest, gin.H{"error": input.Message})
	case errors.Is(err, service.ErrUnsafeURL):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrSignInRequired):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrLinkNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrSlugTaken), errors.Is(err, service.ErrDomainTaken):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrRateLimited):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": fallback})
	}
}

func shortURL(baseURL, slug string) string {
	return baseURL + "/" + slug
}
