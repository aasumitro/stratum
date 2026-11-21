package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IsOperator reports whether this connection logged in as a working operator role.
// It also requires BYPASSRLS, which does not arrive through group membership: a login
// created without it sees no rows on the RLS tables, so a broadcast would reach nobody
// and report success. A server that answers but cannot run the check has given a real
// answer: no audit schema (3F000), no such function (42883), or a role with no access
// to it (42501) all mean this connection is not an attributable operator. Anything
// else, such as a dropped connection or an expired password, says nothing about the
// role, so it comes back as an error for the operator to see.
func IsOperator(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var isOperator bool
	err := pool.QueryRow(ctx, `
		SELECT audit.is_operator(session_user)
		   AND (SELECT r.rolbypassrls FROM pg_catalog.pg_roles r WHERE r.rolname = session_user)
	`).Scan(&isOperator)
	if err == nil {
		return isOperator, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && slices.Contains([]string{"3F000", "42883", "42501"}, pgErr.Code) {
		return false, nil
	}
	return false, fmt.Errorf("query.IsOperator: %w", err)
}

// RecordOperatorAction writes an audit row for an operator action no row trigger
// can see: broker calls, and a broadcast's single logical send.
func RecordOperatorAction(ctx context.Context, pool *pgxpool.Pool, action, resource string, metadata map[string]any) error {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("query.RecordOperatorAction: %w", err)
	}
	if _, err := pool.Exec(ctx, `SELECT audit.record_operator_action($1, $2, $3::jsonb)`, action, resource, string(payload)); err != nil {
		return fmt.Errorf("query.RecordOperatorAction: %w", err)
	}
	return nil
}

type AuditFilter struct {
	Actor          string
	Action         string // HTTP method: GET | POST | PATCH | DELETE | ""
	OrganizationID string
	StatusCode     int        // 0 = no filter
	From           *time.Time // nil = no lower bound
	To             *time.Time // nil = no upper bound
	Limit          int
	Offset         int
}

type AuditRow struct {
	ID             string
	OrganizationID string
	Actor          string
	Action         string
	Resource       string
	StatusCode     int
	IP             string
	CreatedAt      time.Time
}

const auditBaseWhere = `
	WHERE ($1 = '' OR actor ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR action = $2)
	  AND ($3 = '' OR organization_id = $3)
	  AND ($4 = 0   OR status_code = $4)
	  AND ($5::timestamptz IS NULL OR created_at >= $5)
	  AND ($6::timestamptz IS NULL OR created_at <= $6)
`

func auditArgs(f AuditFilter, extra ...any) []any {
	args := []any{f.Actor, f.Action, f.OrganizationID, f.StatusCode, f.From, f.To}
	return append(args, extra...)
}

func ListAuditEvents(ctx context.Context, pool *pgxpool.Pool, f AuditFilter) ([]AuditRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, COALESCE(organization_id,''), actor, action, resource, status_code, ip, created_at
		FROM audit.events`+auditBaseWhere+`
		ORDER BY created_at DESC
		LIMIT $7 OFFSET $8
	`, auditArgs(f, f.Limit, f.Offset)...)
	if err != nil {
		return nil, fmt.Errorf("query.ListAuditEvents: %w", err)
	}
	defer rows.Close()

	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.OrganizationID, &r.Actor, &r.Action, &r.Resource, &r.StatusCode, &r.IP, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("query.ListAuditEvents: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func CountAuditEvents(ctx context.Context, pool *pgxpool.Pool, f AuditFilter) (int, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit.events`+auditBaseWhere,
		auditArgs(f)...,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("query.CountAuditEvents: %w", err)
	}
	return n, nil
}
