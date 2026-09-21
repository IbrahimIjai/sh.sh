package analytics

import "strings"

type UserAgent struct {
	DeviceType string // "mobile", "tablet", "desktop", "bot", or "" when unknown
	Browser    string
	OS         string
	App        string // the app whose in-app browser opened the link, e.g. "Instagram"
	IsBot      bool
}

// Link-preview fetchers (WhatsApp, Slack, X, ...), crawlers, and HTTP libraries. Matched
// against the lowercased user agent.
var botMarkers = []string{
	"bot", "crawler", "spider", "slurp", "facebookexternalhit", "facebookcatalog", "whatsapp",
	"embedly", "preview", "vkshare", "iframely", "headless", "lighthouse", "monitor",
	"curl/", "wget/", "python-", "go-http-client", "okhttp", "java/", "httpclient",
	"axios/", "node-fetch", "undici", "postman", "insomnia",
}

// Apps that open links in their own browser and name themselves in the user agent. Checked in order.
var inAppMarkers = []struct{ marker, app string }{
	{"instagram", "Instagram"},
	{"fban", "Facebook"},
	{"fbav", "Facebook"},
	{"fb_iab", "Facebook"},
	{"linkedinapp", "LinkedIn"},
	{"twitter", "X (Twitter)"},
	{"snapchat", "Snapchat"},
	{"musical_ly", "TikTok"},
	{"bytedancewebview", "TikTok"},
	{"tiktok", "TikTok"},
	{"pinterest", "Pinterest"},
	{"line/", "LINE"},
	{"gsa/", "Google App"},
}

var browserMarkers = []struct{ marker, browser string }{
	{"edg/", "Edge"},
	{"edga/", "Edge"},
	{"edgios/", "Edge"},
	{"opr/", "Opera"},
	{"opera", "Opera"},
	{"samsungbrowser", "Samsung Internet"},
	{"ucbrowser", "UC Browser"},
	{"yabrowser", "Yandex Browser"},
	{"firefox/", "Firefox"},
	{"fxios/", "Firefox"},
	{"crios/", "Chrome"},
	{"chrome/", "Chrome"},
	{"chromium/", "Chrome"},
	{"safari/", "Safari"},
}

// ParseUserAgent is a small, dependency-free parser covering the browsers, platforms, and in-app
// browsers that make up nearly all short-link traffic. Anything else comes back as "".
func ParseUserAgent(raw string) UserAgent {
	ua := strings.ToLower(strings.TrimSpace(raw))

	// Real browsers always send a user agent; "cubot" is a phone brand, not a bot.
	if ua == "" || containsAny(strings.ReplaceAll(ua, "cubot", ""), botMarkers) {
		return UserAgent{DeviceType: "bot", IsBot: true}
	}

	parsed := UserAgent{OS: parseOS(ua)}

	for _, m := range inAppMarkers {
		if strings.Contains(ua, m.marker) {
			parsed.App = m.app
			break
		}
	}

	if parsed.App != "" {
		parsed.Browser = parsed.App + " in-app"
	} else {
		for _, m := range browserMarkers {
			if strings.Contains(ua, m.marker) {
				parsed.Browser = m.browser
				break
			}
		}
	}

	parsed.DeviceType = parseDeviceType(ua, parsed.OS)
	return parsed
}

func parseOS(ua string) string {
	switch {
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"), strings.Contains(ua, "ipod"):
		return "iOS"
	case strings.Contains(ua, "android"):
		return "Android"
	case strings.Contains(ua, "windows"):
		return "Windows"
	case strings.Contains(ua, "cros"):
		return "ChromeOS"
	case strings.Contains(ua, "macintosh"), strings.Contains(ua, "mac os x"):
		return "macOS"
	case strings.Contains(ua, "linux"):
		return "Linux"
	default:
		return ""
	}
}

func parseDeviceType(ua, os string) string {
	switch {
	case strings.Contains(ua, "ipad"), strings.Contains(ua, "tablet"),
		os == "Android" && !strings.Contains(ua, "mobile"):
		return "tablet"
	case strings.Contains(ua, "mobi"), strings.Contains(ua, "iphone"), strings.Contains(ua, "ipod"):
		return "mobile"
	case os == "Windows", os == "macOS", os == "Linux", os == "ChromeOS":
		return "desktop"
	default:
		return ""
	}
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
