package billing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/platform/apperr"
)

// historyView adds a resolved actor (avatar-ready name + kind) on top of the
// raw stored historyRecord — the History tab shows an avatar+name or a
// system/webhook chip, never a raw auth_sub.
type historyView struct {
	historyRecord
	ChangedByName string `json:"changed_by_name"`
	ChangedByKind string `json:"changed_by_kind"` // "user" | "system" | "webhook"
}

func (s *service) listHistory(ctx context.Context, subjectType, subjectID string) ([]historyView, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, apperr.Internal("HISTORY_FETCH_FAILED", "failed to list history", err)
	}
	rows, err := s.repo.listHistory(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return nil, apperr.Internal("HISTORY_FETCH_FAILED", "failed to list history", err)
	}
	out := make([]historyView, len(rows))
	for i, r := range rows {
		name, kind := s.resolveChangedBy(ctx, r.ChangedBy)
		out[i] = historyView{historyRecord: r, ChangedByName: name, ChangedByKind: kind}
	}
	return out, nil
}

// resolveChangedBy turns the raw changed_by value stored on a history row
// into a display name and an actor kind. "system"/"webhook"/"" are the
// literal sentinel values other call sites in this file already pass for
// non-owner-initiated transitions (trial expiry, auto-cancel, payment
// webhook resume) — anything else is expected to be a real auth_sub, looked
// up via userReader. Fails open to the raw value if the reader is unwired
// or the lookup misses, so a stale/unknown auth_sub never breaks the list.
func (s *service) resolveChangedBy(ctx context.Context, changedBy string) (name, kind string) {
	switch changedBy {
	case "", changedBySystem:
		return displayNameSystem, actorKindSystem
	case changedByWebhook:
		return displayNameWebhook, actorKindWebhook
	}
	if s.userReader != nil {
		if u, err := s.userReader.GetUserByAuthSub(ctx, changedBy); err == nil && u != nil {
			if u.Name != "" {
				return u.Name, actorKindUser
			}
			return u.Email, actorKindUser
		}
	}
	return changedBy, actorKindUser
}

func (s *service) anonymizeHistory(ctx context.Context, authSub string) error {
	return s.repo.anonymizeHistory(ctx, s.querier(ctx), authSub)
}
