package auth

import (
	"fmt"
	"html"
)

func magicLinkHTML(url string) string {
	escapedURL := html.EscapeString(url)

	return fmt.Sprintf(`<p>Click the link below to sign in:</p><p><a href="%s">Sign in</a></p><p>If you did not request this email, you can ignore it.</p>`, escapedURL)
}

func magicLinkText(url string) string {
	return fmt.Sprintf("Click the link below to sign in:\n\n%s\n\nIf you did not request this email, you can ignore it.", url)
}
