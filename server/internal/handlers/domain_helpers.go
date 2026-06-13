package handlers

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func normalizeHost(input string) (string, error) {
	raw := strings.TrimSpace(strings.ToLower(input))
	if raw == "" {
		return "", fmt.Errorf("domain is required")
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid domain")
	}

	host := strings.TrimSuffix(parsed.Hostname(), ".")
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return "", fmt.Errorf("invalid domain")
	}

	return host, nil
}
