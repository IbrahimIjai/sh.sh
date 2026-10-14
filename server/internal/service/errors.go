package service

import (
	"errors"

	"github.com/lib/pq"
)

var (
	ErrLinkNotFound   = errors.New("link not found")
	ErrSignInRequired = errors.New("sign in to choose a custom slug")
	ErrRateLimited    = errors.New("too many links from this network; sign in or try again later")
	ErrUnsafeURL      = errors.New("target_url is flagged as unsafe")
	ErrSlugTaken      = errors.New("slug already exists")
	ErrDomainTaken    = errors.New("domain already exists")

	ErrTooManySlugChanges = errors.New("too many slug changes today; try again tomorrow")
)

// InputError is a validation failure whose message is safe to show the client.
type InputError struct {
	Message string
}

func (e *InputError) Error() string {
	return e.Message
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
