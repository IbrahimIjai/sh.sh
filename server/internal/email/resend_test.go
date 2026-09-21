package email

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestResendSend(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"accepted", http.StatusOK, `{"id":"email-id"}`},
		{"invalid API key", http.StatusUnauthorized, `{"message":"API key is invalid"}`},
		{"rate limited", http.StatusTooManyRequests, `{"message":"Too many requests"}`},
		{"unavailable", http.StatusServiceUnavailable, `{"message":"Unavailable"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sender := NewResendSender("test-api-key", "shortened.cv <login@example.com>")
			sender.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.String() != "https://api.resend.com/emails" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
				}
				if got := req.Header.Get("Authorization"); got != "Bearer test-api-key" {
					t.Errorf("Authorization = %q", got)
				}
				if got := req.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q", got)
				}
				var got map[string]any
				if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{
					"from":    "shortened.cv <login@example.com>",
					"to":      []any{"user@example.com"},
					"subject": "Sign in",
					"html":    "<p>Sign in</p>",
					"text":    "Sign in",
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("request body = %#v, want %#v", got, want)
				}
				return &http.Response{
					StatusCode: tt.status,
					Status:     http.StatusText(tt.status),
					Body:       io.NopCloser(strings.NewReader(tt.body)),
				}, nil
			})

			err := sender.Send(context.Background(), "user@example.com", "Sign in", "<p>Sign in</p>", "Sign in")
			if tt.status == http.StatusOK {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.body) {
				t.Fatalf("expected provider error containing %q, got %v", tt.body, err)
			}
		})
	}
}

func TestResendSendCanceled(t *testing.T) {
	sender := NewResendSender("test-api-key", "login@example.com")
	sender.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, "user@example.com", "Sign in", "", "Sign in"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
