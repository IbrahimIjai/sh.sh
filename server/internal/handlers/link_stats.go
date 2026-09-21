package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const (
	defaultStatsDays  = 30
	maxStatsDays      = 365
	statsPerDimension = 10
)

// Breakdown dimensions, as named by ListClickBreakdownByLink.
var statsDimensions = []string{
	"sources", "referrers", "countries", "cities", "devices",
	"browsers", "operating_systems", "utm_sources", "utm_campaigns",
}

// Labels for clicks with no value in a dimension. The UTM dimensions have none: untagged clicks
// are left out of those lists.
var emptyDimensionLabels = map[string]string{
	"sources":           "Direct",
	"referrers":         "Direct",
	"countries":         "Unknown",
	"cities":            "Unknown",
	"devices":           "Unknown",
	"browsers":          "Other",
	"operating_systems": "Other",
}

type statsEntry struct {
	Name   string `json:"name"`
	Clicks int64  `json:"clicks"`
}

type dailyClicks struct {
	Day            string `json:"day"`
	Clicks         int64  `json:"clicks"`
	UniqueVisitors int64  `json:"unique_visitors"`
}

type linkStatsResponse struct {
	LinkID int64  `json:"link_id"`
	Since  string `json:"since"`
	Days   int    `json:"days"`
	// Clicks and UniqueVisitors leave out bots such as link-preview fetchers.
	Clicks         int64                   `json:"clicks"`
	UniqueVisitors int64                   `json:"unique_visitors"`
	BotClicks      int64                   `json:"bot_clicks"`
	Daily          []dailyClicks           `json:"daily"`
	Breakdown      map[string][]statsEntry `json:"breakdown"`
}

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
	link, ok := h.ownedLink(c, userID, id)
	if !ok {
		return
	}

	now := h.now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
	ctx := c.Request.Context()

	totals, err := h.queries.GetClickTotalsByLink(ctx, db.GetClickTotalsByLinkParams{LinkID: link.ID, CreatedAt: since})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load stats"})
		return
	}
	daily, err := h.queries.ListDailyClicksByLink(ctx, db.ListDailyClicksByLinkParams{LinkID: link.ID, CreatedAt: since})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load stats"})
		return
	}
	breakdown, err := h.queries.ListClickBreakdownByLink(ctx, db.ListClickBreakdownByLinkParams{
		LinkID:       link.ID,
		Since:        since,
		PerDimension: statsPerDimension,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load stats"})
		return
	}

	c.JSON(http.StatusOK, linkStatsResponse{
		LinkID:         link.ID,
		Since:          since.Format(time.DateOnly),
		Days:           days,
		Clicks:         totals.Clicks,
		UniqueVisitors: totals.UniqueVisitors,
		BotClicks:      totals.BotClicks,
		Daily:          fillDays(since, days, daily),
		Breakdown:      groupBreakdown(breakdown),
	})
}

// fillDays returns one entry per day, including days without clicks, so charts need no gap handling.
func fillDays(since time.Time, days int, rows []db.ListDailyClicksByLinkRow) []dailyClicks {
	byDay := make(map[string]db.ListDailyClicksByLinkRow, len(rows))
	for _, row := range rows {
		byDay[row.Day.Format(time.DateOnly)] = row
	}

	result := make([]dailyClicks, days)
	for i := range days {
		day := since.AddDate(0, 0, i).Format(time.DateOnly)
		row := byDay[day]
		result[i] = dailyClicks{Day: day, Clicks: row.Clicks, UniqueVisitors: row.UniqueVisitors}
	}
	return result
}

func groupBreakdown(rows []db.ListClickBreakdownByLinkRow) map[string][]statsEntry {
	result := make(map[string][]statsEntry, len(statsDimensions))
	for _, dimension := range statsDimensions {
		result[dimension] = []statsEntry{}
	}

	for _, row := range rows {
		name := row.Name
		if name == "" {
			label, ok := emptyDimensionLabels[row.Dimension]
			if !ok {
				continue
			}
			name = label
		}
		result[row.Dimension] = append(result[row.Dimension], statsEntry{Name: name, Clicks: row.Clicks})
	}
	return result
}
