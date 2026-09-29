package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestLog returns gin's access log for mode "all", only failed requests (status 400 and up)
// for "errors", and nothing for "off". A line per redirect costs CPU at high request rates.
func RequestLog(mode string) gin.HandlerFunc {
	switch mode {
	case "off":
		return func(c *gin.Context) { c.Next() }
	case "errors":
		return gin.LoggerWithConfig(gin.LoggerConfig{
			Skip: func(c *gin.Context) bool { return c.Writer.Status() < http.StatusBadRequest },
		})
	default:
		return gin.Logger()
	}
}
