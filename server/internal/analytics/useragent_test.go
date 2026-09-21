package analytics

import "testing"

func TestParseUserAgent(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want UserAgent
	}{
		{
			name: "iPhone Safari",
			ua:   "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
			want: UserAgent{DeviceType: "mobile", Browser: "Safari", OS: "iOS"},
		},
		{
			name: "Android Chrome",
			ua:   "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36",
			want: UserAgent{DeviceType: "mobile", Browser: "Chrome", OS: "Android"},
		},
		{
			name: "Android tablet",
			ua:   "Mozilla/5.0 (Linux; Android 13; SM-X700) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
			want: UserAgent{DeviceType: "tablet", Browser: "Chrome", OS: "Android"},
		},
		{
			name: "iPad",
			ua:   "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
			want: UserAgent{DeviceType: "tablet", Browser: "Safari", OS: "iOS"},
		},
		{
			name: "Windows Edge",
			ua:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0",
			want: UserAgent{DeviceType: "desktop", Browser: "Edge", OS: "Windows"},
		},
		{
			name: "macOS Firefox",
			ua:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0",
			want: UserAgent{DeviceType: "desktop", Browser: "Firefox", OS: "macOS"},
		},
		{
			name: "Instagram in-app on iOS",
			ua:   "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 Instagram 335.0.0.24.109 (iPhone15,3; iOS 17_5; en_US; en; scale=3.00; 1290x2796; 608474404)",
			want: UserAgent{DeviceType: "mobile", Browser: "Instagram in-app", OS: "iOS", App: "Instagram"},
		},
		{
			name: "Facebook in-app on Android",
			ua:   "Mozilla/5.0 (Linux; Android 14; SM-S918B Build/UP1A.231005.007; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/126.0.6478.71 Mobile Safari/537.36 [FB_IAB/FB4A;FBAV/470.0.0.43.108;]",
			want: UserAgent{DeviceType: "mobile", Browser: "Facebook in-app", OS: "Android", App: "Facebook"},
		},
		{
			name: "Cubot phone is not a bot",
			ua:   "Mozilla/5.0 (Linux; Android 10; CUBOT X30) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36",
			want: UserAgent{DeviceType: "mobile", Browser: "Chrome", OS: "Android"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseUserAgent(tt.ua); got != tt.want {
				t.Errorf("ParseUserAgent() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseUserAgentBots(t *testing.T) {
	bots := []string{
		"",
		"WhatsApp/2.23.20.0 A",
		"facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)",
		"Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)",
		"Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)",
		"Twitterbot/1.0",
		"TelegramBot (like TwitterBot)",
		"LinkedInBot/1.0 (compatible; Mozilla/5.0; Apache-HttpClient +http://www.linkedin.com)",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"curl/8.5.0",
		"python-requests/2.32.3",
		"Go-http-client/1.1",
	}

	for _, ua := range bots {
		got := ParseUserAgent(ua)
		if !got.IsBot || got.DeviceType != "bot" {
			t.Errorf("ParseUserAgent(%q) = %+v, want a bot", ua, got)
		}
	}
}
