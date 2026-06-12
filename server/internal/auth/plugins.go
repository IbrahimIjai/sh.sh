package auth

import (
	"context"
	"log"

	"github.com/ibrahimijai/sh.sh/internal/config"
	"github.com/ibrahimijai/sh.sh/internal/email"
	"github.com/thecodearcher/limen"
	magiclink "github.com/thecodearcher/limen/plugins/magic-link"
	"github.com/thecodearcher/limen/plugins/oauth"
	oauthgoogle "github.com/thecodearcher/limen/plugins/oauth-google"
)

func buildPlugins(cfg config.Config) []limen.Plugin {
	plugins := []limen.Plugin{
		magiclink.New(magiclink.WithSendMagicLink(buildMagicLinkSender(cfg))),
	}

	if cfg.GoogleID == "" || cfg.GoogleSecret == "" {
		log.Println("google auth disabled: GOOGLE_CLIENT_ID or GOOGLE_CLIENT_SECRET is not set")
		return plugins
	}

	return append(plugins, oauth.New(
		oauth.WithProviders(oauthgoogle.New(
			oauthgoogle.WithClientID(cfg.GoogleID),
			oauthgoogle.WithClientSecret(cfg.GoogleSecret),
			oauthgoogle.WithRedirectURL(cfg.GoogleRedirectURL),
		)),
	))
}

func buildMagicLinkSender(cfg config.Config) func(magiclink.MagicLinkMessage) {
	emailSender := email.NewResendSender(cfg.ResendAPIKey, cfg.ResendFrom)
	if emailSender == nil {
		log.Println("resend email disabled: RESEND_API_KEY or RESEND_FROM is not set")
	}

	return func(msg magiclink.MagicLinkMessage) {
		if emailSender == nil {
			log.Printf("magic link for %s: %s", msg.Email, msg.URL)
			return
		}

		if err := emailSender.Send(
			context.Background(),
			msg.Email,
			cfg.MagicLinkEmailSubject,
			magicLinkHTML(msg.URL),
			magicLinkText(msg.URL),
		); err != nil {
			log.Printf("failed to send magic link to %s: %v", msg.Email, err)
			return
		}

		log.Printf("sent magic link to %s", msg.Email)
	}
}
