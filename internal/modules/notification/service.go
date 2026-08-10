package notification

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/mailer"
)

// emailSendTimeout bounds a single mailer.Send call so a hung or slow SMTP
// server can't stall the whole notification consumer queue behind it.
const emailSendTimeout = 15 * time.Second

type service struct {
	repo       *repository
	pool       *pgxpool.Pool
	mailer     *mailer.Mailer
	orgReader  contracts.OrganizationReader
	userReader contracts.UserReader
	appURL     string
	redis      *goredis.Client
}

type listResult struct {
	Messages   []messageRecord
	Total      int64
	NextCursor string
	CursorMode bool
}

// send creates an in-app (or other kind) notification message, honoring the
// recipient's notification.preferences the same way sendEmail does — fails
// open (sends if no preference row), skipped entirely when disabled. No
// check is possible when authSub is nil (e.g. the owner couldn't be
// resolved), so the message is sent as before in that case.
func (s *service) send(
	ctx context.Context,
	organizationID string, authSub *string,
	kind, channel, subject, body string,
	payload json.RawMessage,
) (*messageRecord, error) {
	if authSub != nil {
		allowed, err := s.repo.isPreferenceEnabled(ctx, s.pool, *authSub, kind, channel)
		if err != nil {
			// Fail open: mirrors sendEmail's handling of the identical error —
			// isPreferenceEnabled's own no-rows case already defaults to
			// allowed, so a real lookup error keeps that same default instead
			// of being misread as an explicit opt-out.
			slog.Warn("notification preference check failed, sending anyway",
				"auth_sub", *authSub, "kind", kind, "channel", channel, "error", err)
		} else if !allowed {
			return nil, nil
		}
	}
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	// send is only ever called for in_app messages, where the insert IS the
	// delivery — there's no separate delivery step to wait on, so "sent" is
	// correct immediately, not just a default placeholder.
	sentAt := time.Now()
	msg, err := s.repo.insertMessage(ctx, s.pool, organizationID,
		authSub, kind, channel, subject, body, payload, "sent", &sentAt)
	if err != nil {
		return nil, fmt.Errorf("notification.send: %w", err)
	}
	if kind == "in_app" && authSub != nil && s.redis != nil {
		if err := s.redis.Publish(ctx, "notif:"+*authSub, "1").Err(); err != nil {
			slog.Error("failed to publish notification event", "auth_sub", *authSub, "error", err)
		}
	}
	return msg, nil
}

// claimEvent atomically records that eventID is being processed, returning
// claimed=false if it was already claimed by an earlier delivery of the
// same event (RabbitMQ's at-least-once guarantee means the worker can see
// the same envelope more than once). A single INSERT is already atomic, so
// this doesn't need its own transaction.
func (s *service) claimEvent(ctx context.Context, eventID string) (bool, error) {
	return s.repo.markEventProcessed(ctx, s.pool, eventID)
}

// sendToMany is the batched counterpart to send, for organization-wide
// fan-out (suspend/reactivate/delete) — one preference query, one bulk
// insert, and one Redis pipeline instead of a query+insert+publish per
// recipient. Same fail-open preference semantics as send: an authSub with
// no explicit preference row is treated as enabled.
func (s *service) sendToMany(
	ctx context.Context,
	organizationID string, authSubs []string,
	kind, channel, subject, body string,
	payload json.RawMessage,
) error {
	if len(authSubs) == 0 {
		return nil
	}

	disabled, err := s.repo.listDisabledAuthSubs(ctx, s.pool, authSubs, kind, channel)
	if err != nil {
		return fmt.Errorf("notification.sendToMany: %w", err)
	}

	recipients := make([]string, 0, len(authSubs))
	for _, sub := range authSubs {
		if !disabled[sub] {
			recipients = append(recipients, sub)
		}
	}
	if len(recipients) == 0 {
		return nil
	}

	if payload == nil {
		payload = json.RawMessage("{}")
	}
	sentAt := time.Now()
	msgs, err := s.repo.insertMessages(ctx, s.pool, organizationID,
		recipients, kind, channel, subject, body, payload, "sent", &sentAt)
	if err != nil {
		return fmt.Errorf("notification.sendToMany: %w", err)
	}

	if kind == "in_app" && s.redis != nil {
		pipe := s.redis.Pipeline()
		for _, m := range msgs {
			if m.AuthSub != nil {
				pipe.Publish(ctx, "notif:"+*m.AuthSub, "1")
			}
		}
		if _, err := pipe.Exec(ctx); err != nil {
			slog.Error("failed to publish notification events",
				"organization_id", organizationID, "error", err)
		}
	}
	return nil
}

// sendEmail renders and delivers a templated email. authSub is used to check notification
// preferences — pass empty string to skip the check (e.g. external invite recipients).
func (s *service) sendEmail(
	ctx context.Context,
	organizationID, to, authSub, templateName, lang string,
	data mailer.TemplateData,
) {
	if s.mailer == nil || !s.mailer.IsConfigured() {
		return
	}

	if authSub != "" {
		allowed, err := s.repo.isPreferenceEnabled(ctx, s.pool, authSub, "email", templateName)
		if err != nil {
			// Fail open: a preference-check error must not silently suppress
			// the email indefinitely (isPreferenceEnabled's own no-rows case
			// already defaults to allowed, so this keeps the same default
			// for a real lookup error instead of misreading it as opt-out).
			slog.Warn("notification preference check failed, sending anyway",
				"auth_sub", authSub, "template", templateName, "error", err)
		} else if !allowed {
			return
		}
	}

	subject, html, err := mailer.RenderTemplate(templateName, lang, data)
	if err != nil {
		slog.Warn("email template render failed", "template", templateName, "error", err)
		return
	}

	status := "sent"
	var sentAt *time.Time
	sendCtx, cancel := context.WithTimeout(ctx, emailSendTimeout)
	err = s.mailer.Send(sendCtx, mailer.Message{To: to, Subject: subject, HTML: html})
	cancel()
	if err != nil {
		slog.Warn("email send failed", "to", to,
			"template", templateName, "timeout", emailSendTimeout, "error", err)
		status = "failed"
	} else {
		t := time.Now()
		sentAt = &t
	}

	if _, err := s.repo.insertMessage(
		ctx, s.pool, organizationID, nil, "email", templateName, subject, html,
		json.RawMessage(fmt.Sprintf(`{"to":%q}`, to)), status, sentAt,
	); err != nil {
		slog.Error("failed to record sent email", "organization_id",
			organizationID, "template", templateName, "error", err)
	}
}

// resolveOwnerAuthSub returns a pointer to the organization owner's auth_sub,
// or nil when the organization reader is unavailable or the organization is not found.
func (s *service) resolveOwnerAuthSub(ctx context.Context, organizationID string) *string {
	if s.orgReader == nil {
		return nil
	}
	ws, err := s.orgReader.GetOrganizationByID(ctx, organizationID)
	if err != nil || ws == nil {
		return nil
	}
	sub := ws.OwnerID
	return &sub
}

// resolveOrganizationName looks up an organization's display name. Empty if
// unavailable (reader not wired, or the organization can't be found).
func (s *service) resolveOrganizationName(ctx context.Context, organizationID string) string {
	if s.orgReader == nil {
		return ""
	}
	ws, err := s.orgReader.GetOrganizationByID(ctx, organizationID)
	if err != nil {
		return ""
	}
	return ws.Name
}

// resolveMemberEmail looks up a member's email + personal language
// preference (account.users.preferences.lang). mailer.RenderTemplate falls
// back to "en" on its own when lang is empty or has no matching template,
// so no fallback is applied here.
func (s *service) resolveMemberEmail(ctx context.Context, authSub string) (email, lang string) {
	if s.userReader == nil {
		return "", "en"
	}
	user, err := s.userReader.GetUserByAuthSub(ctx, authSub)
	if err != nil {
		return "", "en"
	}
	return user.Email, user.Lang
}

// resolveOwnerEmail looks up organization owner → profile email + auth_sub.
func (s *service) resolveOwnerEmail(ctx context.Context, organizationID string) (email, name, lang, authSub string) {
	if s.orgReader == nil || s.userReader == nil {
		return "", "", "en", ""
	}
	ws, err := s.orgReader.GetOrganizationByID(ctx, organizationID)
	if err != nil {
		return "", "", "en", ""
	}
	email, lang = s.resolveMemberEmail(ctx, ws.OwnerID)
	return email, ws.Name, lang, ws.OwnerID
}

// resolveFirstOrganizationID anchors a personal (non-org-scoped) notification
// to the auth_sub's earliest-joined organization, since notification.messages.organization_id
// is NOT NULL. Empty string if unavailable — e.g. the reader isn't wired, or
// the user hasn't joined any organization yet (possible between onboarding
// steps: profile is created before an organization is).
func (s *service) resolveFirstOrganizationID(ctx context.Context, authSub string) string {
	if s.orgReader == nil {
		return ""
	}
	id, err := s.orgReader.GetFirstOrganizationIDForMember(ctx, authSub)
	if err != nil {
		return ""
	}
	return id
}

// listForUser lists in_app messages for a user. organizationID and channel
// are optional — empty means all.
//
// Response shape is resolved here and reported back via listResult.CursorMode
// so the handler doesn't have to re-derive it:
//   - a caller-supplied cursor always continues in cursor mode.
//   - pageRequested (the caller explicitly sent a page param) always stays
//     in page mode — Total, never NextCursor — even when the requested page
//     happens to come back exactly full; otherwise an explicit page-based
//     caller would silently flip to the cursor shape and lose Total.
//   - with neither given, falls back to cursor mode whenever another page
//     exists — the product's own notification feed never sends a cursor on
//     its first request, only on "load more", and relies on NextCursor
//     showing up on that first response to know there's more to load.
func (s *service) listForUser(
	ctx context.Context,
	authSub, organizationID, channel, cursor string,
	limit, offset int,
	pageRequested bool,
) (*listResult, error) {
	messages, err := s.repo.listMessages(ctx, s.pool, authSub, organizationID, channel, cursor, limit, offset)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor") {
			return nil, apperr.Validation("INVALID_CURSOR", "the provided cursor is malformed")
		}
		return nil, apperr.Internal("NOTIFICATIONS_FETCH_FAILED", "failed to list notifications", err)
	}

	nextCursor := ""
	if len(messages) == limit && len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		raw := lastMsg.CreatedAt.Format(time.RFC3339Nano) + "|" + lastMsg.ID
		nextCursor = base64.StdEncoding.EncodeToString([]byte(raw))
	}

	if cursor != "" || (!pageRequested && nextCursor != "") {
		return &listResult{Messages: messages, NextCursor: nextCursor, CursorMode: true}, nil
	}
	total, err := s.repo.countTotal(ctx, s.pool, authSub, organizationID, channel)
	if err != nil {
		return nil, apperr.Internal("NOTIFICATIONS_FETCH_FAILED", "failed to list notifications", err)
	}
	return &listResult{Messages: messages, Total: total}, nil
}

func (s *service) unreadCount(ctx context.Context, authSub, organizationID string) (int64, error) {
	n, err := s.repo.countUnread(ctx, s.pool, authSub, organizationID)
	if err != nil {
		return 0, apperr.Internal("UNREAD_COUNT_FAILED", "failed to count unread", err)
	}
	return n, nil
}

func (s *service) markRead(ctx context.Context, authSub, id string) error {
	if err := s.repo.markRead(ctx, s.pool, authSub, id); err != nil {
		return apperr.Internal("MARK_READ_FAILED", "failed to mark as read", err)
	}
	return nil
}

func (s *service) markAllRead(ctx context.Context, authSub, organizationID string) error {
	if err := s.repo.markAllRead(ctx, s.pool, authSub, organizationID); err != nil {
		return apperr.Internal("MARK_ALL_READ_FAILED", "failed to mark all as read", err)
	}
	return nil
}

func (s *service) deleteAllForUser(ctx context.Context, authSub string) error {
	return s.repo.deleteAllForUser(ctx, s.pool, authSub)
}

func (s *service) listForExport(ctx context.Context, authSub string, limit int) ([]messageRecord, error) {
	return s.repo.listForExport(ctx, s.pool, authSub, limit)
}

func (s *service) listPreferences(ctx context.Context, authSub string) ([]preferenceRecord, error) {
	prefs, err := s.repo.listPreferences(ctx, s.pool, authSub)
	if err != nil {
		return nil, apperr.Internal("PREFS_FETCH_FAILED", "failed to list preferences", err)
	}
	return prefs, nil
}

func (s *service) upsertPreference(
	ctx context.Context, authSub, channel, eventType string, enabled bool,
) (*preferenceRecord, error) {
	pref, err := s.repo.upsertPreference(ctx, s.pool, authSub, channel, eventType, enabled)
	if err != nil {
		return nil, apperr.Internal("PREF_UPDATE_FAILED", "failed to update preference", err)
	}
	return pref, nil
}
