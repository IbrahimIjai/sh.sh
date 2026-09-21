package handlers

import (
	"database/sql"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

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
	// ClaimToken is set only in the response that creates an anonymous link.
	ClaimToken string `json:"claim_token,omitempty"`
}

func (h *LinkHandler) linkResponse(link db.Link) linkResponse {
	return linkResponse{
		ID:          link.ID,
		Slug:        link.Slug,
		ShortURL:    h.shortURL(link.Slug),
		TargetURL:   link.TargetUrl,
		Title:       stringPtr(link.Title),
		Description: stringPtr(link.Description),
		IsActive:    link.IsActive,
		ClickCount:  link.ClickCount,
		ExpiresAt:   timePtr(link.ExpiresAt),
		CreatedAt:   link.CreatedAt,
		UpdatedAt:   link.UpdatedAt,
	}
}

func (h *LinkHandler) linkResponses(links []db.Link) []linkResponse {
	responses := make([]linkResponse, 0, len(links))
	for _, link := range links {
		responses = append(responses, h.linkResponse(link))
	}
	return responses
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
