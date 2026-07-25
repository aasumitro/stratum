package notification

import (
	"context"
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
) (*messageRecord, error) {
	m := new(messageRecord)
	err := q.QueryRow(ctx, `
		INSERT INTO notification.messages
		    (organization_id, auth_sub, kind, channel, subject, body, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, organization_id, auth_sub, kind, channel, subject, body,
		          payload, status, sent_at, read_at, created_at, updated_at`,
		organizationID, authSub, kind, channel, subject, body, payload,
	).Scan(&m.ID, &m.OrganizationID, &m.AuthSub, &m.Kind, &m.Channel,
		&m.Subject, &m.Body, &m.Payload, &m.Status, &m.SentAt, &m.ReadAt,
		&m.CreatedAt, &m.UpdatedAt)
	return m, err
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
) ([]messageRecord, error) {
	rows, err := q.Query(ctx, `
		INSERT INTO notification.messages
		    (organization_id, auth_sub, kind, channel, subject, body, payload)
		SELECT $1, sub, $3, $4, $5, $6, $7 FROM unnest($2::text[]) AS sub
		RETURNING id, organization_id, auth_sub, kind, channel, subject, body,
		          payload, status, sent_at, read_at, created_at, updated_at`,
		organizationID, authSubs, kind, channel, subject, body, payload,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []messageRecord
	for rows.Next() {
		var m messageRecord
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.AuthSub, &m.Kind, &m.Channel,
			&m.Subject, &m.Body, &m.Payload, &m.Status, &m.SentAt, &m.ReadAt,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *repository) listMessages(
	ctx context.Context, q db.Querier,
	authSub, organizationID, channel, cursor string,
	limit, offset int,
) ([]messageRecord, error) {
	conds := []string{"kind = 'in_app'", "auth_sub = $1"}
	args := db.NewArgs(authSub)

	if organizationID != "" {
		conds = append(conds, fmt.Sprintf("organization_id = $%d", args.Add(organizationID)))
	}
	if channel != "" {
		conds = append(conds, fmt.Sprintf("channel = $%d", args.Add(channel)))
	}
	if cursor != "" {
		conds = append(conds, fmt.Sprintf("id < $%d", args.Add(cursor)))
	}

	var tail string
	if cursor != "" {
		tail = fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, args.Add(limit))
	} else {
		limitN, offsetN := args.Add(limit), args.Add(offset)
		tail = fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, limitN, offsetN)
	}

	query := `SELECT id, organization_id, auth_sub, kind, channel, subject, body,
		       payload, status, sent_at, read_at, created_at, updated_at
		FROM notification.messages WHERE ` + strings.Join(conds, " AND ") + tail

	rows, err := q.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
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
		return nil, err
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
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *repository) countTotal(
	ctx context.Context, q db.Querier,
	authSub, organizationID, channel string,
) (int64, error) {
	conds := []string{"kind = 'in_app'", "auth_sub = $1"}
	args := db.NewArgs(authSub)

	if organizationID != "" {
		conds = append(conds, fmt.Sprintf("organization_id = $%d", args.Add(organizationID)))
	}
	if channel != "" {
		conds = append(conds, fmt.Sprintf("channel = $%d", args.Add(channel)))
	}

	var total int64
	return total, q.QueryRow(ctx,
		`SELECT COUNT(*) FROM notification.messages WHERE `+strings.Join(conds, " AND "),
		args.Values()...).Scan(&total)
}

func (r *repository) countUnread(
	ctx context.Context, q db.Querier,
	authSub, organizationID string,
) (int64, error) {
	var n int64
	if organizationID != "" {
		return n, q.QueryRow(ctx,
			`SELECT COUNT(*) FROM notification.messages WHERE kind = 'in_app' AND auth_sub = $1 AND organization_id = $2 AND read_at IS NULL`,
			authSub, organizationID).Scan(&n)
	}
	return n, q.QueryRow(ctx,
		`SELECT COUNT(*) FROM notification.messages WHERE kind = 'in_app' AND auth_sub = $1 AND read_at IS NULL`,
		authSub).Scan(&n)
}

// deleteAllForUser deletes every notification message addressed to a user —
// used by the GDPR account-deletion flow.
func (r *repository) deleteAllForUser(ctx context.Context, q db.Querier, authSub string) error {
	_, err := q.Exec(ctx, `DELETE FROM notification.messages WHERE auth_sub = $1`, authSub)
	return err
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
	return err
}

func (r *repository) markAllRead(ctx context.Context, q db.Querier, authSub, organizationID string) error {
	if organizationID != "" {
		_, err := q.Exec(ctx, `
			UPDATE notification.messages
			SET read_at = now(), updated_at = now()
			WHERE auth_sub = $1 AND organization_id = $2 AND kind = 'in_app' AND read_at IS NULL`,
			authSub, organizationID,
		)
		return err
	}
	_, err := q.Exec(ctx, `
		UPDATE notification.messages
		SET read_at = now(), updated_at = now()
		WHERE auth_sub = $1 AND kind = 'in_app' AND read_at IS NULL`,
		authSub,
	)
	return err
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
		return nil, err
	}
	defer rows.Close()

	var out []preferenceRecord
	for rows.Next() {
		var p preferenceRecord
		if err := rows.Scan(&p.ID, &p.Channel, &p.EventType, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
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
	return p, err
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
	return enabled, err
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
		return nil, err
	}
	defer rows.Close()

	disabled := make(map[string]bool)
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, err
		}
		disabled[sub] = true
	}
	return disabled, rows.Err()
}
