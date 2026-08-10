package notification

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type messageRecord struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id"`
	AuthSub        *string         `json:"auth_sub,omitempty"`
	Kind           string          `json:"kind"`
	Channel        string          `json:"channel"`
	Subject        string          `json:"subject"`
	Body           string          `json:"body"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	SentAt         *time.Time      `json:"sent_at,omitempty"`
	ReadAt         *time.Time      `json:"read_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type repository struct{}

func (r *repository) insertMessage(
	ctx context.Context, q db.Querier,
	organizationID string, authSub *string,
	kind, channel, subject, body string,
	payload json.RawMessage,
	status string, sentAt *time.Time,
) (*messageRecord, error) {
	m := new(messageRecord)
	err := q.QueryRow(ctx, `
		INSERT INTO notification.messages
		    (organization_id, auth_sub, kind, channel, subject, body, payload, status, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, organization_id, auth_sub, kind, channel, subject, body,
		          payload, status, sent_at, read_at, created_at, updated_at`,
		organizationID, authSub, kind, channel, subject, body, payload, status, sentAt,
	).Scan(&m.ID, &m.OrganizationID, &m.AuthSub, &m.Kind, &m.Channel,
		&m.Subject, &m.Body, &m.Payload, &m.Status, &m.SentAt, &m.ReadAt,
		&m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("notification.insertMessage: %w", err)
	}
	return m, nil
}

// markEventProcessed inserts eventID into notification.processed_events.
// Returns true when the row was newly inserted, false when it already
// existed (a redelivery of an event this worker already handled). Mirrors
// billing's markWebhookProcessed — same dedup shape, different source.
func (r *repository) markEventProcessed(ctx context.Context, q db.Querier, eventID string) (bool, error) {
	tag, err := q.Exec(ctx,
		`INSERT INTO notification.processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`,
		eventID)
	if err != nil {
		return false, fmt.Errorf("notification.markEventProcessed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// insertMessages bulk-inserts one message per authSub — used by the
// organization-wide fan-out handlers (org suspended/reactivated/deleted) to
// avoid an insert-per-member round trip. unnest($2) pairs each authSub with
// the same fixed column values, mirroring the account module's
// findUsersByAuthSubs use of = ANY($1) for the equivalent batched-read case.
func (r *repository) insertMessages(
	ctx context.Context, q db.Querier,
	organizationID string, authSubs []string,
	kind, channel, subject, body string,
	payload json.RawMessage,
	status string, sentAt *time.Time,
) ([]messageRecord, error) {
	rows, err := q.Query(ctx, `
		INSERT INTO notification.messages
		    (organization_id, auth_sub, kind, channel, subject, body, payload, status, sent_at)
		SELECT $1, sub, $3, $4, $5, $6, $7, $8, $9 FROM unnest($2::text[]) AS sub
		RETURNING id, organization_id, auth_sub, kind, channel, subject, body,
		          payload, status, sent_at, read_at, created_at, updated_at`,
		organizationID, authSubs, kind, channel, subject, body, payload, status, sentAt,
	)
	if err != nil {
		return nil, fmt.Errorf("notification.insertMessages: %w", err)
	}
	defer rows.Close()

	var out []messageRecord
	for rows.Next() {
		var m messageRecord
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.AuthSub, &m.Kind, &m.Channel,
			&m.Subject, &m.Body, &m.Payload, &m.Status, &m.SentAt, &m.ReadAt,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("notification.insertMessages: scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notification.insertMessages: %w", err)
	}
	return out, nil
}

// buildListMessagesFilters builds the shared WHERE-clause fragment for a
// user's in_app messages — organizationID/channel/cursor are all optional
// filters, used by both listMessages (paginated feed) and countTotal (the
// matching count for offset pagination, which never has a cursor).
func buildListMessagesFilters(authSub, organizationID, channel, cursor string) ([]string, *db.Args, error) {
	conds := []string{"kind = 'in_app'", "auth_sub = $1"}
	args := db.NewArgs(authSub)

	if organizationID != "" {
		conds = append(conds, fmt.Sprintf("organization_id = $%d", args.Add(organizationID)))
	}
	if channel != "" {
		conds = append(conds, fmt.Sprintf("channel = $%d", args.Add(channel)))
	}
	if cursor != "" {
		b, err := base64.StdEncoding.DecodeString(cursor)
		if err != nil {
			return nil, nil, fmt.Errorf("notification.listMessages: invalid cursor: %w", err)
		}
		parts := strings.SplitN(string(b), "|", 2)
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("notification.listMessages: invalid cursor format")
		}
		ts, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return nil, nil, fmt.Errorf("notification.listMessages: invalid cursor timestamp: %w", err)
		}
		conds = append(conds, fmt.Sprintf("(created_at, id) < ($%d, $%d)", args.Add(ts), args.Add(parts[1])))
	}
	return conds, args, nil
}

func (r *repository) listMessages(
	ctx context.Context, q db.Querier,
	authSub, organizationID, channel, cursor string,
	limit, offset int,
) ([]messageRecord, error) {
	conds, args, err := buildListMessagesFilters(authSub, organizationID, channel, cursor)
	if err != nil {
		return nil, err
	}

	var tail string
	if cursor != "" {
		tail = fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, args.Add(limit))
	} else {
		limitN, offsetN := args.Add(limit), args.Add(offset)
		tail = fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, limitN, offsetN)
	}

	query := `SELECT id, organization_id, auth_sub, kind, channel, subject, body,
		       payload, status, sent_at, read_at, created_at, updated_at
		FROM notification.messages WHERE ` + strings.Join(conds, " AND ") + tail

	rows, err := q.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, fmt.Errorf("notification.listMessages: %w", err)
	}
	defer rows.Close()

	var out []messageRecord
	for rows.Next() {
		var m messageRecord
		if err := rows.Scan(
			&m.ID, &m.OrganizationID, &m.AuthSub, &m.Kind, &m.Channel,
			&m.Subject, &m.Body, &m.Payload, &m.Status, &m.SentAt, &m.ReadAt,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("notification.listMessages: scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notification.listMessages: %w", err)
	}
	return out, nil
}

// listForExport returns up to limit of a user's most recent notification
// messages across every kind/channel, newest first — used by the GDPR
// data-export flow. Unlike listMessages (scoped to kind='in_app' for the
// in-app notification feed), this has no kind/channel filter at all.
func (r *repository) listForExport(
	ctx context.Context, q db.Querier, authSub string, limit int,
) ([]messageRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, organization_id, auth_sub, kind, channel, subject, body,
		       payload, status, sent_at, read_at, created_at, updated_at
		FROM notification.messages
		WHERE auth_sub = $1
		ORDER BY created_at DESC
		LIMIT $2`,
		authSub, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("notification.listForExport: %w", err)
	}
	defer rows.Close()

	var out []messageRecord
	for rows.Next() {
		var m messageRecord
		if err := rows.Scan(
			&m.ID, &m.OrganizationID, &m.AuthSub, &m.Kind, &m.Channel,
			&m.Subject, &m.Body, &m.Payload, &m.Status, &m.SentAt, &m.ReadAt,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("notification.listForExport: scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notification.listForExport: %w", err)
	}
	return out, nil
}

func (r *repository) countTotal(
	ctx context.Context, q db.Querier,
	authSub, organizationID, channel string,
) (int64, error) {
	conds, args, err := buildListMessagesFilters(authSub, organizationID, channel, "")
	if err != nil {
		return 0, err
	}

	var total int64
	if err := q.QueryRow(ctx,
		`SELECT COUNT(*) FROM notification.messages WHERE `+strings.Join(conds, " AND "),
		args.Values()...).Scan(&total); err != nil {
		return 0, fmt.Errorf("notification.countTotal: %w", err)
	}
	return total, nil
}

func (r *repository) countUnread(
	ctx context.Context, q db.Querier,
	authSub, organizationID string,
) (int64, error) {
	var n int64
	var err error
	if organizationID != "" {
		err = q.QueryRow(ctx,
			`SELECT COUNT(*) FROM notification.messages WHERE kind = 'in_app' AND auth_sub = $1 AND organization_id = $2 AND read_at IS NULL`,
			authSub, organizationID).Scan(&n)
	} else {
		err = q.QueryRow(ctx,
			`SELECT COUNT(*) FROM notification.messages WHERE kind = 'in_app' AND auth_sub = $1 AND read_at IS NULL`,
			authSub).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("notification.countUnread: %w", err)
	}
	return n, nil
}

// deleteAllForUser deletes every notification message addressed to a user —
// used by the GDPR account-deletion flow.
func (r *repository) deleteAllForUser(ctx context.Context, q db.Querier, authSub string) error {
	if _, err := q.Exec(ctx, `DELETE FROM notification.messages WHERE auth_sub = $1`, authSub); err != nil {
		return fmt.Errorf("notification.deleteAllForUser: %w", err)
	}
	return nil
}

// markRead is scoped to auth_sub so a caller can only mark their own
// notifications read.
func (r *repository) markRead(ctx context.Context, q db.Querier, authSub, id string) error {
	_, err := q.Exec(ctx, `
		UPDATE notification.messages
		SET read_at = now(), updated_at = now()
		WHERE id = $1 AND auth_sub = $2 AND kind = 'in_app' AND read_at IS NULL`,
		id, authSub,
	)
	if err != nil {
		return fmt.Errorf("notification.markRead: %w", err)
	}
	return nil
}

func (r *repository) markAllRead(ctx context.Context, q db.Querier, authSub, organizationID string) error {
	var err error
	if organizationID != "" {
		_, err = q.Exec(ctx, `
			UPDATE notification.messages
			SET read_at = now(), updated_at = now()
			WHERE auth_sub = $1 AND organization_id = $2 AND kind = 'in_app' AND read_at IS NULL`,
			authSub, organizationID,
		)
	} else {
		_, err = q.Exec(ctx, `
			UPDATE notification.messages
			SET read_at = now(), updated_at = now()
			WHERE auth_sub = $1 AND kind = 'in_app' AND read_at IS NULL`,
			authSub,
		)
	}
	if err != nil {
		return fmt.Errorf("notification.markAllRead: %w", err)
	}
	return nil
}

type preferenceRecord struct {
	ID        string    `json:"id"`
	Channel   string    `json:"channel"`
	EventType string    `json:"event_type"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *repository) listPreferences(ctx context.Context, q db.Querier, authSub string) ([]preferenceRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, channel, event_type, enabled, created_at, updated_at
		FROM notification.preferences
		WHERE auth_sub = $1
		ORDER BY channel, event_type`,
		authSub,
	)
	if err != nil {
		return nil, fmt.Errorf("notification.listPreferences: %w", err)
	}
	defer rows.Close()

	var out []preferenceRecord
	for rows.Next() {
		var p preferenceRecord
		if err := rows.Scan(&p.ID, &p.Channel, &p.EventType, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("notification.listPreferences: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notification.listPreferences: %w", err)
	}
	return out, nil
}

func (r *repository) upsertPreference(
	ctx context.Context, q db.Querier,
	authSub, channel, eventType string, enabled bool,
) (*preferenceRecord, error) {
	p := new(preferenceRecord)
	err := q.QueryRow(ctx, `
		INSERT INTO notification.preferences (auth_sub, channel, event_type, enabled)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (auth_sub, channel, event_type) DO UPDATE
		SET enabled = EXCLUDED.enabled, updated_at = now()
		RETURNING id, channel, event_type, enabled, created_at, updated_at`,
		authSub, channel, eventType, enabled,
	).Scan(&p.ID, &p.Channel, &p.EventType, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("notification.upsertPreference: %w", err)
	}
	return p, nil
}

// isPreferenceEnabled returns true by default when no explicit preference row exists (opt-out model).
func (r *repository) isPreferenceEnabled(
	ctx context.Context, q db.Querier,
	authSub, channel, eventType string,
) (bool, error) {
	var enabled bool
	err := q.QueryRow(ctx, `
		SELECT enabled FROM notification.preferences
		WHERE auth_sub = $1 AND channel = $2 AND event_type = $3`,
		authSub, channel, eventType,
	).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("notification.isPreferenceEnabled: %w", err)
	}
	return enabled, nil
}

// listDisabledAuthSubs is the batched counterpart to isPreferenceEnabled —
// one query for a whole fan-out instead of one per recipient. Same opt-out
// model: a missing row means enabled, so this only needs to report who
// explicitly opted out; every authSub not in the returned set is enabled.
func (r *repository) listDisabledAuthSubs(
	ctx context.Context, q db.Querier,
	authSubs []string, channel, eventType string,
) (map[string]bool, error) {
	rows, err := q.Query(ctx, `
		SELECT auth_sub FROM notification.preferences
		WHERE auth_sub = ANY($1) AND channel = $2 AND event_type = $3 AND enabled = false`,
		authSubs, channel, eventType,
	)
	if err != nil {
		return nil, fmt.Errorf("notification.listDisabledAuthSubs: %w", err)
	}
	defer rows.Close()

	disabled := make(map[string]bool)
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, fmt.Errorf("notification.listDisabledAuthSubs: scan: %w", err)
		}
		disabled[sub] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notification.listDisabledAuthSubs: %w", err)
	}
	return disabled, nil
}
