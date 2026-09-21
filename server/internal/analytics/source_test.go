package analytics

import "testing"

func TestRefererHostAndSource(t *testing.T) {
	const ownHost = "shortened.cv"

	tests := []struct {
		referer    string
		app        string
		wantHost   string
		wantSource string
	}{
		{referer: "", wantHost: "", wantSource: "Direct"},
		{referer: "", app: "Instagram", wantHost: "", wantSource: "Instagram"},
		{referer: "https://t.co/abc123", wantHost: "t.co", wantSource: "X (Twitter)"},
		{referer: "https://l.facebook.com/l.php?u=x", wantHost: "l.facebook.com", wantSource: "Facebook"},
		{referer: "https://www.linkedin.com/feed/", wantHost: "linkedin.com", wantSource: "LinkedIn"},
		{referer: "https://www.google.co.uk/", wantHost: "google.co.uk", wantSource: "Google"},
		{referer: "https://mail.google.com/mail/u/0/", wantHost: "mail.google.com", wantSource: "Gmail"},
		{referer: "android-app://com.google.android.gm/", wantHost: "com.google.android.gm", wantSource: "Gmail"},
		{referer: "https://news.ycombinator.com/item?id=1", wantHost: "news.ycombinator.com", wantSource: "Hacker News"},
		{referer: "https://blog.example.com/post", wantHost: "blog.example.com", wantSource: "blog.example.com"},
		// Parent-domain matching is by label, not substring.
		{referer: "https://notfacebook.com/", wantHost: "notfacebook.com", wantSource: "notfacebook.com"},
		// A referrer takes priority over the in-app browser.
		{referer: "https://t.co/x", app: "Instagram", wantHost: "t.co", wantSource: "X (Twitter)"},
		// Our own pages don't count as a source.
		{referer: "https://www.shortened.cv/abc+", wantHost: "", wantSource: "Direct"},
		{referer: "::not a url", wantHost: "", wantSource: "Direct"},
	}

	for _, tt := range tests {
		host := RefererHost(tt.referer, ownHost)
		if host != tt.wantHost {
			t.Errorf("RefererHost(%q) = %q, want %q", tt.referer, host, tt.wantHost)
		}
		if source := Source(host, tt.app); source != tt.wantSource {
			t.Errorf("Source(%q, %q) = %q, want %q", host, tt.app, source, tt.wantSource)
		}
	}
}

func TestRefererHostComparesPort(t *testing.T) {
	if got := RefererHost("http://localhost:8080/abc+", "localhost:8080"); got != "" {
		t.Errorf("own host with port = %q, want empty", got)
	}
	if got := RefererHost("http://localhost:3000/", "localhost:8080"); got != "localhost" {
		t.Errorf("other port on localhost = %q, want localhost", got)
	}
}
