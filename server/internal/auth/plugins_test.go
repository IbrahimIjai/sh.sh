package auth

import (
	"reflect"
	"testing"

	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/email"
)

func TestBuildEmailSender(t *testing.T) {
	for _, tt := range []struct {
		name     string
		provider string
		missing  string
		want     email.Sender
	}{
		{name: "default", want: (*email.ResendSender)(nil)},
		{name: "resend", provider: "resend", want: (*email.ResendSender)(nil)},
		{name: "transend", provider: "transend", want: (*email.TransendSender)(nil)},
		{name: "resend without key", provider: "resend", missing: "resend key"},
		{name: "resend without from", provider: "resend", missing: "resend from"},
		{name: "transend without key", provider: "transend", missing: "transend key"},
		{name: "transend without from", provider: "transend", missing: "transend from"},
		{name: "unknown provider", provider: "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				EmailProvider:  tt.provider,
				ResendAPIKey:   "resend-test-key",
				ResendFrom:     "resend@example.com",
				TransendAPIKey: "transend-test-key",
				TransendFrom:   "transend@example.com",
			}
			switch tt.missing {
			case "resend key":
				cfg.ResendAPIKey = ""
			case "resend from":
				cfg.ResendFrom = ""
			case "transend key":
				cfg.TransendAPIKey = ""
			case "transend from":
				cfg.TransendFrom = ""
			}
			got := buildEmailSender(cfg)
			if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
				t.Fatalf("sender type = %T, want %T", got, tt.want)
			}
		})
	}
}
