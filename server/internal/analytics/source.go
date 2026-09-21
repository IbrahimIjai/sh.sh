package analytics

import (
	"net/url"
	"strings"
)

// Friendly names for referrer hosts, matched on the host or any parent domain
// (l.facebook.com matches facebook.com). Android apps send android-app://<package> referrers,
// so a few package names are here too.
var knownSources = map[string]string{
	"t.co":        "X (Twitter)",
	"twitter.com": "X (Twitter)",
	"x.com":       "X (Twitter)",

	"facebook.com":  "Facebook",
	"fb.com":        "Facebook",
	"fb.me":         "Facebook",
	"messenger.com": "Messenger",
	"instagram.com": "Instagram",
	"threads.net":   "Threads",
	"threads.com":   "Threads",

	"linkedin.com":         "LinkedIn",
	"lnkd.in":              "LinkedIn",
	"com.linkedin.android": "LinkedIn",

	"youtube.com":   "YouTube",
	"youtu.be":      "YouTube",
	"reddit.com":    "Reddit",
	"tiktok.com":    "TikTok",
	"pinterest.com": "Pinterest",
	"pin.it":        "Pinterest",
	"snapchat.com":  "Snapchat",

	"whatsapp.com":           "WhatsApp",
	"t.me":                   "Telegram",
	"telegram.org":           "Telegram",
	"org.telegram.messenger": "Telegram",
	"discord.com":            "Discord",
	"discordapp.com":         "Discord",
	"slack.com":              "Slack",
	"com.slack":              "Slack",

	"mail.google.com":       "Gmail",
	"com.google.android.gm": "Gmail",
	"outlook.live.com":      "Outlook",
	"outlook.office.com":    "Outlook",
	"outlook.office365.com": "Outlook",
	"mail.yahoo.com":        "Yahoo Mail",

	"com.google.android.googlequicksearchbox": "Google",
	"bing.com":         "Bing",
	"duckduckgo.com":   "DuckDuckGo",
	"search.yahoo.com": "Yahoo",
	"search.brave.com": "Brave Search",
	"yandex.ru":        "Yandex",
	"yandex.com":       "Yandex",

	"news.ycombinator.com": "Hacker News",
	"github.com":           "GitHub",
	"chatgpt.com":          "ChatGPT",
	"perplexity.ai":        "Perplexity",
}

// RefererHost returns the lowercased hostname of a Referer header, without "www.". It returns ""
// when there is no usable referrer or it is ownHost (host[:port]), e.g. a click on our own
// preview page.
func RefererHost(referer, ownHost string) string {
	if referer == "" {
		return ""
	}
	parsed, err := url.Parse(referer)
	if err != nil || withoutWWW(parsed.Host) == withoutWWW(ownHost) {
		return ""
	}
	return withoutWWW(parsed.Hostname())
}

func withoutWWW(host string) string {
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

// Source names where a click came from: the referring site, else the app whose in-app
// browser opened the link, else "Direct". Many apps (WhatsApp, email, SMS) send no referrer,
// so "Direct" also covers those.
func Source(refererHost, app string) string {
	if refererHost != "" {
		if name := knownSource(refererHost); name != "" {
			return name
		}
		return refererHost
	}
	if app != "" {
		return app
	}
	return "Direct"
}

func knownSource(host string) string {
	for candidate := host; candidate != ""; {
		if name, ok := knownSources[candidate]; ok {
			return name
		}
		_, parent, found := strings.Cut(candidate, ".")
		if !found {
			break
		}
		candidate = parent
	}
	// google.com, google.co.uk, google.com.ng, ...
	if strings.Contains("."+host+".", ".google.") {
		return "Google"
	}
	return ""
}
