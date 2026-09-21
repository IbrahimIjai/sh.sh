package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const transendEndpoint = "https://api.transend.dev/v1/emails"

type TransendSender struct {
	apiKey string
	from   string
	client *http.Client
}

func NewTransendSender(apiKey, from string) *TransendSender {
	if apiKey == "" || from == "" {
		return nil
	}

	return &TransendSender{
		apiKey: apiKey,
		from:   from,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *TransendSender) Send(ctx context.Context, to, subject, htmlBody, textBody string) error {
	if s == nil {
		return fmt.Errorf("transend sender is not configured")
	}

	body, err := json.Marshal(sendEmailRequest{
		From:    s.from,
		To:      []string{to},
		Subject: subject,
		HTML:    htmlBody,
		Text:    textBody,
	})
	if err != nil {
		return fmt.Errorf("marshal transend request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transendEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create transend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "shortened-cv-api/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send transend request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("transend returned %s: %s", resp.Status, string(responseBody))
	}

	return nil
}
