package auth

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

const UserIDKey = "userID"

func (s *Server) RequireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || s.limen == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "auth is not configured"})
			return
		}

		session, err := s.limen.GetSession(c.Request)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		userID, err := int64ID(session.User.ID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid session user"})
			return
		}

		c.Set(UserIDKey, userID)
		c.Next()
	}
}

// OptionalSession sets the user ID when the request carries a valid session and lets
// everyone else through as anonymous.
func (s *Server) OptionalSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || s.limen == nil {
			c.Next()
			return
		}

		if session, err := s.limen.GetSession(c.Request); err == nil {
			if userID, err := int64ID(session.User.ID); err == nil {
				c.Set(UserIDKey, userID)
			}
		}
		c.Next()
	}
}

func int64ID(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported id type %T", value)
	}
}
