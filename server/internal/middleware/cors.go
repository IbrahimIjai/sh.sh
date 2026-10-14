package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS lets the UI at appURL call the API and auth routes with the user's cookies.
// Preflights never reach a route: gin sends unregistered OPTIONS requests to NoRoute.
func CORS(appURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if appURL == "" || !(strings.HasPrefix(path, "/api/") || path == "/auth" || strings.HasPrefix(path, "/auth/")) {
			c.Next()
			return
		}

		header := c.Writer.Header()
		header.Add("Vary", "Origin")
		if c.GetHeader("Origin") != appURL {
			c.Next()
			return
		}

		header.Set("Access-Control-Allow-Origin", appURL)
		header.Set("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions {
			header.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE")
			header.Set("Access-Control-Allow-Headers", "Content-Type")
			header.Set("Access-Control-Max-Age", "86400")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
