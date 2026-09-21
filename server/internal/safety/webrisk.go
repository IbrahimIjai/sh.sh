package safety

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const webRiskEndpoint = "https://webrisk.googleapis.com/v1/uris:search"

// WebRisk checks URLs against Google's Web Risk lists (the commercial counterpart of the
// Safe Browsing API). https://cloud.google.com/web-risk/docs/lookup-api
type WebRisk struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

func NewWebRisk(apiKey string) *WebRisk {
	return &WebRisk{
		apiKey:   apiKey,
		endpoint: webRiskEndpoint,
		client:   &http.Client{Timeout: 3 * time.Second},
	}
}

type webRiskResponse struct {
	Threat struct {
		ThreatTypes []string `json:"threatTypes"`
	} `json:"threat"`
}

// Check returns the threat types Web Risk lists for rawURL, or nil when it's not listed.
func (w *WebRisk) Check(ctx context.Context, rawURL string) ([]string, error) {
	query := url.Values{}
	query.Add("threatTypes", "MALWARE")
	query.Add("threatTypes", "SOCIAL_ENGINEERING")
	query.Add("threatTypes", "UNWANTED_SOFTWARE")
	query.Set("uri", rawURL)
	query.Set("key", w.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create web risk request: %w", err)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		// The request URL carries the API key; drop it from the error so it can't reach the logs.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("send web risk request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("web risk returned %s: %s", resp.Status, string(body))
	}

	var result webRiskResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode web risk response: %w", err)
	}
	return result.Threat.ThreatTypes, nil
}
