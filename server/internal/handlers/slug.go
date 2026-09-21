package handlers

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
)

const slugAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Custom slugs can't contain "/" or "+" (the redirect handler reads "/{slug}+" as a preview) and
// can't shadow a route, or the link would be saved but never reachable.
var customSlugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

var reservedSlugs = map[string]bool{
	"api": true, "auth": true, "health": true, "admin": true, "login": true, "logout": true,
	"signin": true, "signup": true, "settings": true, "dashboard": true, "static": true, "assets": true,
}

func validSlug(slug string) bool {
	return customSlugPattern.MatchString(slug) && !reservedSlugs[strings.ToLower(slug)]
}

func randomSlug(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate slug: %w", err)
	}

	for i, b := range bytes {
		bytes[i] = slugAlphabet[int(b)%len(slugAlphabet)]
	}

	return string(bytes), nil
}
