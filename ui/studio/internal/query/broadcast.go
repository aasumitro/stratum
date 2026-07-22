package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BroadcastRecipient struct {
	AuthSub        string
	OrganizationID string
}

// CountBroadcastRecipients returns the number of unique users for the given target.
func CountBroadcastRecipients(ctx context.Context, pool *pgxpool.Pool, target string) (int, error) {
	switch {
	case target == "all":
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM account.users`).Scan(&n); err != nil {
			return 0, fmt.Errorf("query.CountBroadcastRecipients: %w", err)
		}
		return n, nil

	case strings.HasPrefix(target, "organization:"):
		oid := strings.TrimPrefix(target, "organization:")
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(DISTINCT auth_sub) FROM organization.memberships WHERE organization_id = $1`, oid,
		).Scan(&n); err != nil {
			return 0, fmt.Errorf("query.CountBroadcastRecipients: %w", err)
		}
		return n, nil

	case strings.HasPrefix(target, "user:"):
		authSub := strings.TrimPrefix(target, "user:")
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM organization.memberships WHERE auth_sub = $1)
		`, authSub).Scan(&exists); err != nil {
			return 0, fmt.Errorf("query.CountBroadcastRecipients: %w", err)
		}
		if exists {
			return 1, nil
		}
		return 0, nil

	default:
		return 0, fmt.Errorf("query.CountBroadcastRecipients: unknown target %q", target)
	}
}

// ResolveBroadcastRecipients returns (auth_sub, organization_id) pairs for the given target.
// For "all" and user targets, the primary (earliest-joined) organization is used.
func ResolveBroadcastRecipients(ctx context.Context, pool *pgxpool.Pool, target string) ([]BroadcastRecipient, error) {
	switch {
	case target == "all":
		rows, err := pool.Query(ctx, `
			SELECT DISTINCT ON (auth_sub) auth_sub, organization_id
			FROM organization.memberships
			ORDER BY auth_sub, joined_at ASC
		`)
		if err != nil {
			return nil, fmt.Errorf("query.ResolveBroadcastRecipients all: %w", err)
		}
		defer rows.Close()
		return scanRecipients(rows)

	case strings.HasPrefix(target, "organization:"):
		oid := strings.TrimPrefix(target, "organization:")
		rows, err := pool.Query(ctx, `
			SELECT DISTINCT auth_sub, $1::uuid AS organization_id
			FROM organization.memberships
			WHERE organization_id = $1
		`, oid)
		if err != nil {
			return nil, fmt.Errorf("query.ResolveBroadcastRecipients organization: %w", err)
		}
		defer rows.Close()
		return scanRecipients(rows)

	case strings.HasPrefix(target, "user:"):
		authSub := strings.TrimPrefix(target, "user:")
		var oid string
		if err := pool.QueryRow(ctx, `
			SELECT organization_id FROM organization.memberships
			WHERE auth_sub = $1 ORDER BY joined_at ASC LIMIT 1
		`, authSub).Scan(&oid); err != nil {
			return nil, fmt.Errorf("query.ResolveBroadcastRecipients user: %w", err)
		}
		return []BroadcastRecipient{{AuthSub: authSub, OrganizationID: oid}}, nil

	default:
		return nil, fmt.Errorf("query.ResolveBroadcastRecipients: unknown target %q", target)
	}
}

func scanRecipients(rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
}) ([]BroadcastRecipient, error) {
	var out []BroadcastRecipient
	for rows.Next() {
		var r BroadcastRecipient
		if err := rows.Scan(&r.AuthSub, &r.OrganizationID); err != nil {
			return nil, fmt.Errorf("query.scanRecipients: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertBroadcastNotification inserts one in-app notification for a single recipient.
func InsertBroadcastNotification(ctx context.Context, pool *pgxpool.Pool, r BroadcastRecipient, subject, body string) error {
	id := uuid.New().String()
	_, err := pool.Exec(ctx, `
		INSERT INTO notification.messages
			(id, organization_id, auth_sub, kind, channel, subject, body, status, created_at, updated_at)
		VALUES ($1, $2::uuid, $3, 'in_app', 'broadcast', $4, $5, 'sent', now(), now())
	`, id, r.OrganizationID, r.AuthSub, subject, body)
	if err != nil {
		return fmt.Errorf("query.InsertBroadcastNotification: %w", err)
	}
	return nil
}
