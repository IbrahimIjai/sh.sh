package analytics

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fakeLocator struct {
	country, city string
	got           netip.Addr
}

func (f *fakeLocator) Locate(ip netip.Addr) (string, string) {
	f.got = ip
	return f.country, f.city
}

func TestHasher(t *testing.T) {
	h := NewHasher("secret")
	day := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)

	if h.Visitor("1.2.3.4", "ua", day) != h.Visitor("1.2.3.4", "ua", day.Add(10*time.Hour)) {
		t.Error("same visitor on the same UTC day should hash the same")
	}
	if h.Visitor("1.2.3.4", "ua", day) == h.Visitor("1.2.3.4", "ua", day.AddDate(0, 0, 1)) {
		t.Error("visitor hash should change with the day")
	}
	if h.Visitor("1.2.3.4", "ua", day) == h.Visitor("1.2.3.5", "ua", day) {
		t.Error("different IPs should hash differently")
	}
	if h.Creator("1.2.3.4") != h.Creator("1.2.3.4") || h.Creator("1.2.3.4") == h.Creator("1.2.3.5") {
		t.Error("creator hash should be stable per IP and differ between IPs")
	}
	if NewHasher("other").Creator("1.2.3.4") == h.Creator("1.2.3.4") {
		t.Error("hashes should depend on the secret")
	}
	if got := h.Creator("1.2.3.4"); len(got) != 64 || strings.Contains(got, "1.2.3.4") {
		t.Errorf("creator hash %q should be 64 hex chars without the IP", got)
	}
}

func TestCollect(t *testing.T) {
	locator := &fakeLocator{country: "NG", city: "Lagos"}
	collector := NewCollector(CollectorConfig{Hasher: NewHasher("secret"), Locator: locator, OwnHost: "shortened.cv"})

	req := httptest.NewRequest("GET", "/abc?utm_source=newsletter&utm_medium=email&utm_campaign=launch", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1")
	req.Header.Set("Referer", "https://t.co/xyz")
	req.Header.Set("CF-IPCountry", "US") // ignored: Cloudflare is off

	click := collector.Collect(req, "::ffff:102.89.1.1", 42)

	if click.LinkID != 42 || click.Source != "X (Twitter)" || click.RefererHost != "t.co" {
		t.Errorf("unexpected source fields: %+v", click)
	}
	if click.UTMSource != "newsletter" || click.UTMMedium != "email" || click.UTMCampaign != "launch" {
		t.Errorf("unexpected utm fields: %+v", click)
	}
	if click.DeviceType != "mobile" || click.Browser != "Safari" || click.OS != "iOS" || click.IsBot {
		t.Errorf("unexpected device fields: %+v", click)
	}
	if click.Country != "NG" || click.City != "Lagos" {
		t.Errorf("location = %q/%q, want NG/Lagos", click.Country, click.City)
	}
	if locator.got.String() != "102.89.1.1" {
		t.Errorf("locator got %s, want the IPv4 address unmapped", locator.got)
	}
	if len(click.VisitorHash) != 64 {
		t.Errorf("visitor hash %q should be set", click.VisitorHash)
	}
}

func TestCollectCloudflareHeaders(t *testing.T) {
	locator := &fakeLocator{country: "DE"}
	collector := NewCollector(CollectorConfig{Hasher: NewHasher("secret"), Locator: locator, Cloudflare: true})

	req := httptest.NewRequest("GET", "/abc", nil)
	req.Header.Set("CF-IPCountry", "gh")
	req.Header.Set("CF-IPCity", "Accra")
	if click := collector.Collect(req, "1.2.3.4", 1); click.Country != "GH" || click.City != "Accra" {
		t.Errorf("location = %q/%q, want GH/Accra from Cloudflare", click.Country, click.City)
	}

	// Tor and unknown fall back to the local database.
	req.Header.Set("CF-IPCountry", "T1")
	if click := collector.Collect(req, "1.2.3.4", 1); click.Country != "DE" {
		t.Errorf("country = %q, want DE from the locator", click.Country)
	}
}

func TestTruncateKeepsUTF8Valid(t *testing.T) {
	if got := truncate("São Paulo", 2); got != "S" {
		t.Errorf("truncate = %q, want %q", got, "S")
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("truncate = %q, want abc", got)
	}
}
