package account

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// LegalDocument is a document a user must accept: "terms" or "privacy", its
// current version, and where its text lives.
type LegalDocument struct {
	Name    string
	Version string
	URL     string
}

// consentStatus is one required document as the caller sees it: the current
// version, and the version they last accepted, if any.
type consentStatus struct {
	Document        string     `json:"document"`
	Version         string     `json:"version"`
	URL             string     `json:"url"`
	AcceptedVersion *string    `json:"accepted_version,omitempty"`
	AcceptedAt      *time.Time `json:"accepted_at,omitempty"`
}

// consentRecord is one acceptance, as kept and as exported.
type consentRecord struct {
	Document   string    `json:"document"`
	Version    string    `json:"version"`
	AcceptedAt time.Time `json:"accepted_at"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
}

func (r *repository) insertConsent(ctx context.Context, q db.Querier, authSub, document, version, ip, userAgent string) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO account.consents (auth_sub, document, version, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)`,
		authSub, document, version, ip, userAgent,
	); err != nil {
		return fmt.Errorf("account.insertConsent: %w", err)
	}
	return nil
}

// listConsents returns every acceptance of authSub's, oldest first.
func (r *repository) listConsents(ctx context.Context, q db.Querier, authSub string) ([]consentRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT document, version, accepted_at, ip, user_agent
		FROM account.consents WHERE auth_sub = $1
		ORDER BY accepted_at, id`, authSub)
	if err != nil {
		return nil, fmt.Errorf("account.listConsents: %w", err)
	}
	defer rows.Close()
	out := []consentRecord{}
	for rows.Next() {
		var c consentRecord
		if err := rows.Scan(&c.Document, &c.Version, &c.AcceptedAt, &c.IP, &c.UserAgent); err != nil {
			return nil, fmt.Errorf("account.listConsents: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account.listConsents: %w", err)
	}
	return out, nil
}

func (r *repository) deleteConsents(ctx context.Context, q db.Querier, authSub string) error {
	if _, err := q.Exec(ctx, `DELETE FROM account.consents WHERE auth_sub = $1`, authSub); err != nil {
		return fmt.Errorf("account.deleteConsents: %w", err)
	}
	return nil
}

// consentStatus lists each required document with the caller's latest
// acceptance of it. Empty when no document is required.
func (s *service) consentStatus(ctx context.Context, authSub string) ([]consentStatus, error) {
	out := make([]consentStatus, 0, len(s.legal))
	if len(s.legal) == 0 {
		return out, nil
	}
	records, err := s.repo.listConsents(ctx, s.pool, authSub)
	if err != nil {
		return nil, apperr.Internal("CONSENT_STATUS_FAILED", "failed to read accepted documents", err)
	}
	latest := map[string]consentRecord{}
	for _, r := range records {
		latest[r.Document] = r
	}
	for _, d := range s.legal {
		st := consentStatus{Document: d.Name, Version: d.Version, URL: d.URL}
		if r, ok := latest[d.Name]; ok {
			st.AcceptedVersion = &r.Version
			st.AcceptedAt = &r.AcceptedAt
		}
		out = append(out, st)
	}
	return out, nil
}

// acceptConsent records that the caller accepted version of document. Only a
// required document's current version can be accepted, so a stale page cannot
// record agreement to text that is no longer in force.
func (s *service) acceptConsent(ctx context.Context, authSub, document, version, ip, userAgent string) error {
	for _, d := range s.legal {
		if d.Name != document {
			continue
		}
		if version != d.Version {
			return apperr.Conflict("CONSENT_VERSION_OUTDATED", "a newer version of this document is in force; accept that one")
		}
		if err := s.repo.insertConsent(ctx, s.pool, authSub, document, version, ip, userAgent); err != nil {
			return apperr.Internal("CONSENT_RECORD_FAILED", "failed to record the acceptance", err)
		}
		return nil
	}
	return apperr.Validation("CONSENT_DOCUMENT_UNKNOWN", "this document does not need to be accepted")
}
