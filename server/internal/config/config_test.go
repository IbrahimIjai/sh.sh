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
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
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

func TestLoadDatabasePoolConfig(t *testing.T) {
	for _, tt := range []struct {
		name, open, idle   string
		wantOpen, wantIdle int
		wantErr            bool
	}{
		{"defaults", "", "", 20, 20, false},
		{"smaller pool", "5", "", 5, 5, false},
		{"explicit", "16", "8", 16, 8, false},
		{"no idle", "16", "0", 16, 0, false},
		{"unlimited rejected", "0", "", 0, 0, true},
		{"negative rejected", "-1", "", 0, 0, true},
		{"negative idle", "20", "-1", 0, 0, true},
		{"idle above cap", "5", "6", 0, 0, true},
		{"malformed open", "many", "", 0, 0, true},
		{"malformed idle", "20", "many", 0, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DB_MAX_OPEN_CONNS", tt.open)
			t.Setenv("DB_MAX_IDLE_CONNS", tt.idle)
			cfg, err := Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && (cfg.DBMaxOpenConns != tt.wantOpen || cfg.DBMaxIdleConns != tt.wantIdle) {
				t.Fatalf("pool = %d/%d, want %d/%d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns, tt.wantOpen, tt.wantIdle)
			}
		})
	}
}
