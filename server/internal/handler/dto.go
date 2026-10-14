package handler

import (
	"database/sql"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/service"
)

type createLinkRequest struct {
	TargetURL   string `json:"target_url" binding:"required"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type changeSlugRequest struct {
	Slug string `json:"slug" binding:"required"`
}

type claimTokensRequest struct {
	ClaimTokens []string `json:"claim_tokens" binding:"required,min=1,max=100"`
}

type createDomainRequest struct {
	Domain string `json:"domain" binding:"required"`
}

// linkResponse is the public shape of a link. It leaves out internal columns (owner, claim
// token hash, creator hash) and flattens sql.Null* values into plain JSON.
type linkResponse struct {
	ID          int64      `json:"id"`
	Slug        string     `json:"slug"`
	ShortURL    string     `json:"short_url"`
	TargetURL   string     `json:"target_url"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	IsActive    bool       `json:"is_active"`
	ClickCount  int64      `json:"click_count"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	// PreviousSlugs still redirect to this link, newest first.
	PreviousSlugs []string `json:"previous_slugs"`
	// ClaimToken is set only in the response that creates an anonymous link.
	ClaimToken string `json:"claim_token,omitempty"`
}

func newLinkResponse(baseURL string, summary service.LinkSummary) linkResponse {
	link := summary.Link
	previous := summary.PreviousSlugs
	if previous == nil {
		previous = []string{}
	}
	return linkResponse{
		ID:            link.ID,
		Slug:          link.Slug,
		ShortURL:      shortURL(baseURL, link.Slug),
		TargetURL:     link.TargetUrl,
		Title:         stringPtr(link.Title),
		Description:   stringPtr(link.Description),
		IsActive:      link.IsActive,
		ClickCount:    summary.Clicks,
		ExpiresAt:     timePtr(link.ExpiresAt),
		CreatedAt:     link.CreatedAt,
		UpdatedAt:     link.UpdatedAt,
		PreviousSlugs: previous,
	}
}

func newLinkResponses(baseURL string, links []service.LinkSummary) []linkResponse {
	responses := make([]linkResponse, 0, len(links))
	for _, summary := range links {
		responses = append(responses, newLinkResponse(baseURL, summary))
	}
	return responses
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
	LinkID         int64                   `json:"link_id"`
	Since          string                  `json:"since"`
	Days           int                     `json:"days"`
	Clicks         int64                   `json:"clicks"`
	UniqueVisitors int64                   `json:"unique_visitors"`
	BotClicks      int64                   `json:"bot_clicks"`
	Daily          []dailyClicks           `json:"daily"`
	Breakdown      map[string][]statsEntry `json:"breakdown"`
}

func newLinkStatsResponse(stats service.LinkStats) linkStatsResponse {
	daily := make([]dailyClicks, len(stats.Daily))
	for i, day := range stats.Daily {
		daily[i] = dailyClicks{Day: day.Day.Format(time.DateOnly), Clicks: day.Clicks, UniqueVisitors: day.UniqueVisitors}
	}

	breakdown := make(map[string][]statsEntry, len(stats.Breakdown))
	for dimension, entries := range stats.Breakdown {
		list := make([]statsEntry, len(entries))
		for i, entry := range entries {
			list[i] = statsEntry{Name: entry.Name, Clicks: entry.Clicks}
		}
		breakdown[dimension] = list
	}

	return linkStatsResponse{
		LinkID:         stats.LinkID,
		Since:          stats.Since.Format(time.DateOnly),
		Days:           stats.Days,
		Clicks:         stats.Clicks,
		UniqueVisitors: stats.UniqueVisitors,
		BotClicks:      stats.BotClicks,
		Daily:          daily,
		Breakdown:      breakdown,
	}
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func timePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
