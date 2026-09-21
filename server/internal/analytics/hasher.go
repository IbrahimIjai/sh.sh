package analytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Hasher turns IP addresses into keyed hashes so raw IPs never reach the database.
type Hasher struct {
	key []byte
}

func NewHasher(secret string) *Hasher {
	// Derive a separate key so these hashes don't share a key with anything else signed by the secret.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("sh.sh/analytics/v1"))
	return &Hasher{key: mac.Sum(nil)}
}

// Visitor identifies one visitor for one UTC day. The day is part of the input, so the same
// person gets an unrelated hash tomorrow and visits can't be joined across days.
func (h *Hasher) Visitor(ip, userAgent string, at time.Time) string {
	return h.sum("visitor", at.UTC().Format(time.DateOnly), ip, userAgent)
}

// Creator identifies whoever creates anonymous links from an IP, for rate limiting.
func (h *Hasher) Creator(ip string) string {
	return h.sum("creator", ip)
}

func (h *Hasher) sum(parts ...string) string {
	mac := hmac.New(sha256.New, h.key)
	for _, part := range parts {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}
