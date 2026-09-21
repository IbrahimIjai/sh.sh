package handlers

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/auth"
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

func validURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
