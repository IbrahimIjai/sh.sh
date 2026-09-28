package service

import (
	"context"
	"net"
	"net/url"
	"strings"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

const verificationTokenLength = 32

type DomainService struct {
	queries db.Querier
}

func NewDomainService(queries db.Querier) *DomainService {
	return &DomainService{queries: queries}
}

func (s *DomainService) Create(ctx context.Context, userID int64, domain string) (db.Domain, error) {
	host, err := normalizeHost(domain)
	if err != nil {
		return db.Domain{}, err
	}

	token, err := randomSlug(verificationTokenLength)
	if err != nil {
		return db.Domain{}, err
	}

	created, err := s.queries.CreateDomain(ctx, db.CreateDomainParams{
		UserID:            userID,
		Host:              host,
		VerificationToken: token,
	})
	if isUniqueViolation(err) {
		return db.Domain{}, ErrDomainTaken
	}
	return created, err
}

func (s *DomainService) List(ctx context.Context, userID int64) ([]db.Domain, error) {
	return s.queries.ListDomainsByUser(ctx, userID)
}

func (s *DomainService) Delete(ctx context.Context, userID, id int64) error {
	return s.queries.SoftDeleteDomain(ctx, db.SoftDeleteDomainParams{ID: id, UserID: userID})
}

func normalizeHost(input string) (string, error) {
	raw := strings.TrimSpace(strings.ToLower(input))
	if raw == "" {
		return "", &InputError{Message: "domain is required"}
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", &InputError{Message: "invalid domain"}
	}

	host := strings.TrimSuffix(parsed.Hostname(), ".")
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return "", &InputError{Message: "invalid domain"}
	}

	return host, nil
}
