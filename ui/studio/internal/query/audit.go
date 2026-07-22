package query

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
