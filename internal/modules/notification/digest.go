package notification

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/mailer"
)

// digestTemplates are informational emails a user may take as one daily digest
// instead of one by one. Anything that needs action, or concerns security or
// money owed, is always sent at once.
var digestTemplates = map[string]bool{
	"invoice_paid":           true,
	"usage_limit_warning":    true,
	"webhook_health_warning": true,
}

const (
	// digestEventType is the notification.preferences row (channel "email")
	// that turns the digest on. No row means off: the digest is opt-in.
	digestEventType = "digest"
	// digestHour is the local hour after which the day's digest goes out.
	digestHour = 8
)

type heldEmail struct {
	ID             string
	OrganizationID string
	Subject        string
	Summary        string
	CreatedAt      time.Time
}

func (r *repository) isDigestEnabled(ctx context.Context, q db.Querier, authSub string) (bool, error) {
	var enabled bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM notification.preferences
		    WHERE auth_sub = $1 AND channel = 'email' AND event_type = $2 AND enabled)`,
		authSub, digestEventType,
	).Scan(&enabled)
	if err != nil {
		return false, fmt.Errorf("notification.isDigestEnabled: %w", err)
	}
	return enabled, nil
}

func (r *repository) listHeldAuthSubs(ctx context.Context, q db.Querier) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT auth_sub FROM notification.messages WHERE status = 'held'`)
	if err != nil {
		return nil, fmt.Errorf("notification.listHeldAuthSubs: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("notification.listHeldAuthSubs: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// listHeldBefore returns authSub's held emails created before cutoff, oldest first.
func (r *repository) listHeldBefore(ctx context.Context, q db.Querier, authSub string, cutoff time.Time) ([]heldEmail, error) {
	rows, err := q.Query(ctx, `
		SELECT id, organization_id, subject, body, created_at
		FROM notification.messages
		WHERE status = 'held' AND auth_sub = $1 AND created_at < $2
		ORDER BY created_at, id`, authSub, cutoff)
	if err != nil {
		return nil, fmt.Errorf("notification.listHeldBefore: %w", err)
	}
	defer rows.Close()
	var out []heldEmail
	for rows.Next() {
		var h heldEmail
		if err := rows.Scan(&h.ID, &h.OrganizationID, &h.Subject, &h.Summary, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("notification.listHeldBefore: scan: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// markHeld moves held rows to status: 'sent' (with sentAt) or 'failed'.
func (r *repository) markHeld(ctx context.Context, q db.Querier, ids []string, status string, sentAt *time.Time) error {
	if _, err := q.Exec(ctx, `
		UPDATE notification.messages SET status = $2, sent_at = $3, updated_at = now()
		WHERE id = ANY($1) AND status = 'held'`, ids, status, sentAt); err != nil {
		return fmt.Errorf("notification.markHeld: %w", err)
	}
	return nil
}

// failAllHeld gives up on everything held for authSub: there is no address to
// send it to.
func (r *repository) failAllHeld(ctx context.Context, q db.Querier, authSub string) error {
	if _, err := q.Exec(ctx, `
		UPDATE notification.messages SET status = 'failed', updated_at = now()
		WHERE auth_sub = $1 AND status = 'held'`, authSub); err != nil {
		return fmt.Errorf("notification.failAllHeld: %w", err)
	}
	return nil
}

// holdForDigest records an eligible email as held when its recipient takes
// the digest. It reports whether it held the email; on any failure the caller
// sends the email at once, so nothing is lost.
func (s *service) holdForDigest(
	ctx context.Context, organizationID, to, authSub, templateName, subject, summary string,
) bool {
	if authSub == "" || !digestTemplates[templateName] {
		return false
	}
	on, err := s.repo.isDigestEnabled(ctx, s.pool, authSub)
	if err != nil {
		slog.Warn("digest preference check failed, sending now", "auth_sub", authSub, "error", err)
		return false
	}
	if !on {
		return false
	}
	// The body holds the one-line summary the digest lists under the subject.
	if _, err := s.repo.insertMessage(ctx, s.pool, organizationID, &authSub, "email", templateName,
		subject, cmp.Or(summary, subject), json.RawMessage(fmt.Sprintf(`{"to":%q}`, to)), "held", nil); err != nil {
		slog.Warn("holding email for digest failed, sending now", "auth_sub", authSub, "error", err)
		return false
	}
	return true
}

// digestCutoff is the latest digestHour:00 in tz at or before now: emails held
// before it are due. An unknown or empty zone counts as UTC.
func digestCutoff(now time.Time, tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if tz == "" || err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	cut := time.Date(local.Year(), local.Month(), local.Day(), digestHour, 0, 0, 0, loc)
	if local.Before(cut) {
		cut = cut.AddDate(0, 0, -1)
	}
	return cut
}

// sendDueDigests sends each user with held emails one digest of those held
// before their latest local digestHour:00. A failed send leaves them held, so
// the next hourly tick tries again; an item still unsent a full day after it
// came due (held before the previous local digestHour:00) is marked failed.
// Runs on the worker's hourly ticker; one worker replica is assumed, as for
// the other hourly jobs.
func (s *service) sendDueDigests(ctx context.Context, now time.Time) {
	if s.mailer == nil || !s.mailer.IsConfigured() || s.userReader == nil {
		return
	}
	subs, err := s.repo.listHeldAuthSubs(ctx, s.pool)
	if err != nil || len(subs) == 0 {
		if err != nil {
			slog.Warn("digest: list recipients failed", "error", err)
		}
		return
	}
	users, err := s.userReader.GetUsersByAuthSubs(ctx, subs)
	if err != nil {
		slog.Warn("digest: resolve recipients failed", "error", err)
		return
	}
	orgNames := map[string]string{}
	for _, sub := range subs {
		if ctx.Err() != nil {
			return // shutting down; the next start picks up the rest
		}
		u, ok := users[sub]
		if !ok || u.Email == "" {
			// No address to send to; a deleted account's rows go with it anyway.
			if err := s.repo.failAllHeld(ctx, s.pool, sub); err != nil {
				slog.Warn("digest: fail held emails without an address", "auth_sub", sub, "error", err)
			}
			continue
		}
		loc, _ := time.LoadLocation(u.Timezone)
		if loc == nil {
			loc = time.UTC
		}
		cutoff := digestCutoff(now, u.Timezone)
		held, err := s.repo.listHeldBefore(ctx, s.pool, sub, cutoff)
		if err != nil {
			slog.Warn("digest: list held emails failed", "auth_sub", sub, "error", err)
			continue
		}
		if len(held) == 0 {
			continue
		}
		items := make([]mailer.DigestItem, len(held))
		ids := make([]string, len(held))
		for i, h := range held {
			name, seen := orgNames[h.OrganizationID]
			if !seen {
				name = s.resolveOrganizationName(ctx, h.OrganizationID)
				orgNames[h.OrganizationID] = name
			}
			summary := h.Summary
			if summary == h.Subject {
				summary = ""
			}
			items[i] = mailer.DigestItem{
				Subject:          h.Subject,
				Summary:          summary,
				OrganizationName: name,
				Date:             h.CreatedAt.In(loc).Format("2 Jan 2006 15:04"),
			}
			ids[i] = h.ID
		}
		subject, html, err := mailer.RenderTemplate("digest", u.Lang, mailer.TemplateData{
			ActionURL:   s.appURL + "/notifications",
			DigestItems: items,
		})
		if err != nil {
			slog.Warn("digest: render failed", "auth_sub", sub, "lang", u.Lang, "error", err)
			continue
		}
		// An in-flight digest finishes even on shutdown, so it is never cut off
		// after the server took it and then sent again on the next start.
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), emailSendTimeout)
		err = s.mailer.Send(sendCtx, mailer.Message{To: u.Email, Subject: subject, HTML: html})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down: give nothing up, try again on the next start
			}
			slog.Warn("digest: send failed, kept for the next tick", "auth_sub", sub, "error", err)
			// Items held before the previous local 08:00 have been due for a
			// full day of hourly tries (DST-safe: both cutoffs are local times).
			prev := digestCutoff(cutoff.Add(-time.Nanosecond), u.Timezone)
			var stale []string
			for _, h := range held {
				if h.CreatedAt.Before(prev) {
					stale = append(stale, h.ID)
				}
			}
			if len(stale) > 0 {
				slog.Warn("digest: giving up on items due for a day", "auth_sub", sub, "count", len(stale))
				if mErr := s.repo.markHeld(context.WithoutCancel(ctx), s.pool, stale, "failed", nil); mErr != nil {
					slog.Error("digest: mark failed", "auth_sub", sub, "error", mErr)
				}
			}
			continue
		}
		// The email is out: record it even if the worker is shutting down, or
		// the next tick would send it again.
		sentAt := now
		if err := s.repo.markHeld(context.WithoutCancel(ctx), s.pool, ids, "sent", &sentAt); err != nil {
			slog.Error("digest: sent but not marked", "auth_sub", sub, "error", err)
		}
	}
}

// SendDueDigests is the worker's hourly entry point for the daily email digest.
func (m *Module) SendDueDigests(ctx context.Context) {
	m.svc.sendDueDigests(ctx, time.Now())
}
