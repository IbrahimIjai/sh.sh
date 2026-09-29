package analytics

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"
)

// Click is one visit to a short link. The raw IP, user agent and full referrer URL are already
// reduced to a daily visitor hash and parsed fields. String fields are "" when unknown.
type Click struct {
	// EventID and CreatedAt identify the click in storage: writing it again is a no-op.
	EventID     string
	CreatedAt   time.Time
	LinkID      int64
	VisitorHash string
	RefererHost string
	Source      string
	UTMSource   string
	UTMMedium   string
	UTMCampaign string
	DeviceType  string
	Browser     string
	OS          string
	Country     string
	City        string
	IsBot       bool
}

type CollectorConfig struct {
	Hasher *Hasher
	// Locator looks up country/city from the client IP. Nil disables IP lookups.
	Locator Locator
	// Cloudflare trusts the CF-IPCountry / CF-IPCity headers. Only enable it when every request
	// reaches the API through Cloudflare; otherwise clients can set those headers themselves.
	Cloudflare bool
	// OwnHost is the short-link host; referrers from it are ignored.
	OwnHost string
}

// Collector turns a redirect request into a Click.
type Collector struct {
	cfg CollectorConfig
	now func() time.Time
}

func NewCollector(cfg CollectorConfig) *Collector {
	return &Collector{cfg: cfg, now: time.Now}
}

func (c *Collector) Collect(r *http.Request, clientIP string, linkID int64) Click {
	userAgent := r.UserAgent()
	ua := ParseUserAgent(userAgent)
	refererHost := RefererHost(r.Referer(), c.cfg.OwnHost)
	query := r.URL.Query()
	country, city := c.locate(r, clientIP)
	now := c.now().Truncate(time.Microsecond)

	return Click{
		EventID:     NewEventID(),
		CreatedAt:   now,
		LinkID:      linkID,
		VisitorHash: c.cfg.Hasher.Visitor(clientIP, userAgent, now),
		RefererHost: truncate(refererHost, 255),
		Source:      truncate(Source(refererHost, ua.App), 255),
		UTMSource:   truncate(query.Get("utm_source"), 255),
		UTMMedium:   truncate(query.Get("utm_medium"), 255),
		UTMCampaign: truncate(query.Get("utm_campaign"), 255),
		DeviceType:  ua.DeviceType,
		Browser:     ua.Browser,
		OS:          ua.OS,
		Country:     country,
		City:        truncate(city, 128),
		IsBot:       ua.IsBot,
	}
}

func (c *Collector) locate(r *http.Request, clientIP string) (string, string) {
	if c.cfg.Cloudflare {
		// XX means unknown and T1 means Tor.
		country := strings.ToUpper(r.Header.Get("CF-IPCountry"))
		if len(country) == 2 && country != "XX" && country != "T1" {
			return country, r.Header.Get("CF-IPCity")
		}
	}
	if c.cfg.Locator == nil {
		return "", ""
	}
	ip, err := netip.ParseAddr(clientIP)
	if err != nil {
		return "", ""
	}
	country, city := c.cfg.Locator.Locate(ip.Unmap())
	if len(country) != 2 {
		country = ""
	}
	return country, city
}

// truncate cuts s to at most n bytes without splitting a UTF-8 character, so values always fit
// their column and one oversized value can't fail a whole batch insert.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// NewEventID returns a random UUID (version 4).
func NewEventID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:], b[10:])
	return string(out[:])
}
