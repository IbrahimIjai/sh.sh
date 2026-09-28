package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	defaultStatsDays = 30
	maxStatsDays     = 365
)

// Stats returns click analytics for one of the user's links over the last ?days= days (UTC),
// 30 by default.
func (h *LinkHandler) Stats(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	days, err := strconv.Atoi(c.DefaultQuery("days", strconv.Itoa(defaultStatsDays)))
	if err != nil || days < 1 || days > maxStatsDays {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days must be between 1 and " + strconv.Itoa(maxStatsDays)})
		return
	}

	stats, err := h.links.Stats(c.Request.Context(), userID, id, days)
	if err != nil {
		writeError(c, err, "could not load stats")
		return
	}

	c.JSON(http.StatusOK, newLinkStatsResponse(stats))
}
