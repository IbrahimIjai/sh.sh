package config

import "testing"

func TestLoadEmailConfig(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "resend-test-key")
	t.Setenv("RESEND_FROM", "resend@example.com")
	t.Setenv("TRANSEND_API_KEY", "transend-test-key")
	t.Setenv("TRANSEND_FROM", "transend@example.com")

	for _, tt := range []struct {
		provider string
		want     string
	}{
		{"", "resend"},
		{"resend", "resend"},
		{" TRANSEND ", "transend"},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			t.Setenv("EMAIL_PROVIDER", tt.provider)
			cfg := Load()
			if cfg.EmailProvider != tt.want {
				t.Errorf("EmailProvider = %q, want %q", cfg.EmailProvider, tt.want)
			}
			if cfg.ResendAPIKey != "resend-test-key" || cfg.ResendFrom != "resend@example.com" {
				t.Error("Resend settings were not loaded")
			}
			if cfg.TransendAPIKey != "transend-test-key" || cfg.TransendFrom != "transend@example.com" {
				t.Error("Transend settings were not loaded")
			}
		})
	}
}
