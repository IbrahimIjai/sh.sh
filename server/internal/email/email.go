package email

import "context"

type Sender interface {
	Send(ctx context.Context, to, subject, htmlBody, textBody string) error
}

type sendEmailRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Text    string   `json:"text"`
}
