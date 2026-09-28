package service

import (
	"context"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const statsPerDimension = 10

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

type DayClicks struct {
	Day            time.Time
	Clicks         int64
	UniqueVisitors int64
}

type NamedClicks struct {
	Name   string
	Clicks int64
}

type LinkStats struct {
	LinkID int64
	Since  time.Time
	Days   int
	// Clicks and UniqueVisitors leave out bots such as link-preview fetchers.
	Clicks         int64
	UniqueVisitors int64
	BotClicks      int64
	Daily          []DayClicks
	Breakdown      map[string][]NamedClicks
}

// Stats returns click analytics for one of the user's links over the last `days` UTC days,
// today included.
func (s *LinkService) Stats(ctx context.Context, userID, linkID int64, days int) (LinkStats, error) {
	link, err := s.owned(ctx, userID, linkID)
	if err != nil {
		return LinkStats{}, err
	}

	now := s.now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))

	totals, err := s.queries.GetClickTotalsByLink(ctx, db.GetClickTotalsByLinkParams{LinkID: link.ID, CreatedAt: since})
	if err != nil {
		return LinkStats{}, err
	}
	daily, err := s.queries.ListDailyClicksByLink(ctx, db.ListDailyClicksByLinkParams{LinkID: link.ID, CreatedAt: since})
	if err != nil {
		return LinkStats{}, err
	}
	breakdown, err := s.queries.ListClickBreakdownByLink(ctx, db.ListClickBreakdownByLinkParams{
		LinkID:       link.ID,
		Since:        since,
		PerDimension: statsPerDimension,
	})
	if err != nil {
		return LinkStats{}, err
	}

	return LinkStats{
		LinkID:         link.ID,
		Since:          since,
		Days:           days,
		Clicks:         totals.Clicks,
		UniqueVisitors: totals.UniqueVisitors,
		BotClicks:      totals.BotClicks,
		Daily:          fillDays(since, days, daily),
		Breakdown:      groupBreakdown(breakdown),
	}, nil
}

// fillDays returns one entry per day, including days without clicks, so charts need no gap handling.
func fillDays(since time.Time, days int, rows []db.ListDailyClicksByLinkRow) []DayClicks {
	byDay := make(map[string]db.ListDailyClicksByLinkRow, len(rows))
	for _, row := range rows {
		byDay[row.Day.Format(time.DateOnly)] = row
	}

	result := make([]DayClicks, days)
	for i := range days {
		day := since.AddDate(0, 0, i)
		row := byDay[day.Format(time.DateOnly)]
		result[i] = DayClicks{Day: day, Clicks: row.Clicks, UniqueVisitors: row.UniqueVisitors}
	}
	return result
}

func groupBreakdown(rows []db.ListClickBreakdownByLinkRow) map[string][]NamedClicks {
	result := make(map[string][]NamedClicks, len(statsDimensions))
	for _, dimension := range statsDimensions {
		result[dimension] = []NamedClicks{}
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
		result[row.Dimension] = append(result[row.Dimension], NamedClicks{Name: name, Clicks: row.Clicks})
	}
	return result
}
