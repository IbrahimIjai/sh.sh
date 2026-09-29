package service

import (
	"cmp"
	"context"
	"slices"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const statsPerDimension = 10

// Breakdown dimensions, as named by the click dimension queries.
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
// today included. Days with rollups are read from them; the rest from raw clicks.
func (s *LinkService) Stats(ctx context.Context, userID, linkID int64, days int) (LinkStats, error) {
	link, err := s.owned(ctx, userID, linkID)
	if err != nil {
		return LinkStats{}, err
	}
	if s.reportSlots != nil {
		select {
		case s.reportSlots <- struct{}{}:
			defer func() { <-s.reportSlots }()
		case <-ctx.Done():
			return LinkStats{}, ctx.Err()
		}
	}

	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(days - 1))
	rawFrom, err := s.rawFrom(ctx, since, today)
	if err != nil {
		return LinkStats{}, err
	}

	totals, err := s.queries.GetClickTotalsByLink(ctx, db.GetClickTotalsByLinkParams{LinkID: link.ID, CreatedAt: rawFrom})
	if err != nil {
		return LinkStats{}, err
	}
	daily, err := s.queries.ListDailyClicksByLink(ctx, db.ListDailyClicksByLinkParams{LinkID: link.ID, CreatedAt: rawFrom})
	if err != nil {
		return LinkStats{}, err
	}
	dimensions, err := s.queries.ListClickDimensionsByLink(ctx, db.ListClickDimensionsByLinkParams{LinkID: link.ID, Since: rawFrom})
	if err != nil {
		return LinkStats{}, err
	}

	if rawFrom.After(since) {
		rolled := db.GetRollupTotalsByLinkParams{LinkID: link.ID, Since: since, Until: rawFrom}
		rolledTotals, err := s.queries.GetRollupTotalsByLink(ctx, rolled)
		if err != nil {
			return LinkStats{}, err
		}
		totals.Clicks += rolledTotals.Clicks
		totals.UniqueVisitors += rolledTotals.UniqueVisitors
		totals.BotClicks += rolledTotals.BotClicks

		rolledDaily, err := s.queries.ListRollupDailyByLink(ctx, db.ListRollupDailyByLinkParams(rolled))
		if err != nil {
			return LinkStats{}, err
		}
		for _, row := range rolledDaily {
			daily = append(daily, db.ListDailyClicksByLinkRow(row))
		}

		rolledDimensions, err := s.queries.ListRollupDimensionsByLink(ctx, db.ListRollupDimensionsByLinkParams(rolled))
		if err != nil {
			return LinkStats{}, err
		}
		for _, row := range rolledDimensions {
			dimensions = append(dimensions, db.ListClickDimensionsByLinkRow(row))
		}
	}

	return LinkStats{
		LinkID:         link.ID,
		Since:          since,
		Days:           days,
		Clicks:         totals.Clicks,
		UniqueVisitors: totals.UniqueVisitors,
		BotClicks:      totals.BotClicks,
		Daily:          fillDays(since, days, daily),
		Breakdown:      groupBreakdown(dimensions),
	}, nil
}

// rawFrom is the first day from since on without a rollup. Earlier days come from rollups; this
// day and everything after from raw clicks, so a gap never double-counts or skips a day.
func (s *LinkService) rawFrom(ctx context.Context, since, today time.Time) (time.Time, error) {
	days, err := s.queries.ListRolledDays(ctx, db.ListRolledDaysParams{Since: since, Until: today})
	if err != nil {
		return time.Time{}, err
	}
	rolled := make(map[string]bool, len(days))
	for _, d := range days {
		rolled[d.Format(time.DateOnly)] = true
	}
	from := since
	for from.Before(today) && rolled[from.Format(time.DateOnly)] {
		from = from.AddDate(0, 0, 1)
	}
	return from, nil
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

// groupBreakdown sums rows per dimension and value, keeps the top statsPerDimension values (an
// empty value competes like any other), then labels or drops empty values.
func groupBreakdown(rows []db.ListClickDimensionsByLinkRow) map[string][]NamedClicks {
	sums := make(map[string]map[string]int64, len(statsDimensions))
	for _, row := range rows {
		if sums[row.Dimension] == nil {
			sums[row.Dimension] = map[string]int64{}
		}
		sums[row.Dimension][row.Name] += row.Clicks
	}

	result := make(map[string][]NamedClicks, len(statsDimensions))
	for _, dimension := range statsDimensions {
		ranked := make([]NamedClicks, 0, len(sums[dimension]))
		for name, clicks := range sums[dimension] {
			ranked = append(ranked, NamedClicks{Name: name, Clicks: clicks})
		}
		slices.SortFunc(ranked, func(a, b NamedClicks) int {
			if c := cmp.Compare(b.Clicks, a.Clicks); c != 0 {
				return c
			}
			return cmp.Compare(a.Name, b.Name)
		})
		ranked = ranked[:min(len(ranked), statsPerDimension)]

		entries := []NamedClicks{}
		for _, entry := range ranked {
			if entry.Name == "" {
				label, ok := emptyDimensionLabels[dimension]
				if !ok {
					continue
				}
				entry.Name = label
			}
			entries = append(entries, entry)
		}
		result[dimension] = entries
	}
	return result
}
