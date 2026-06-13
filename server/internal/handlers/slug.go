package handlers

import (
	"crypto/rand"
	"fmt"
)

const slugAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

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
