package query

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type OrganizationRow struct {
	ID            string
	Name          string
	Slug          string
	Status        string
	CountryCode   string
	OwnerID       string
	CreatedAt     time.Time
	MemberCount   int
	PlanName      string
	BillingStatus string
}

// ListOrganizations returns organizations filtered by status (empty string = all non-deleted).
func ListOrganizations(ctx context.Context, pool *pgxpool.Pool, status string, limit, offset int) ([]OrganizationRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			t.id,
			t.name,
			t.slug,
			t.status,
			t.country_code,
			t.owner_id,
			t.created_at,
			COUNT(DISTINCT m.auth_sub)        AS member_count,
			COALESCE(p.name, '')              AS plan_name,
			COALESCE(s.status, '')            AS billing_status
		FROM organization.organizations t
		LEFT JOIN organization.memberships m ON m.organization_id = t.id
		LEFT JOIN billing.subscriptions s ON s.subject_id = t.id::text
		LEFT JOIN billing.plans p ON p.id = s.plan
		WHERE ($1 = '' OR t.status = $1)
		GROUP BY t.id, t.name, t.slug, t.status, t.country_code, t.owner_id, t.created_at, p.name, s.status
		ORDER BY t.created_at DESC
		LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query.ListOrganizations: %w", err)
	}
	defer rows.Close()

	var out []OrganizationRow
	for rows.Next() {
		var o OrganizationRow
		if err := rows.Scan(
			&o.ID, &o.Name, &o.Slug, &o.Status, &o.CountryCode,
			&o.OwnerID, &o.CreatedAt, &o.MemberCount, &o.PlanName, &o.BillingStatus,
		); err != nil {
			return nil, fmt.Errorf("query.ListOrganizations: scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CountOrganizations returns the total number of organizations for the given status filter.
func CountOrganizations(ctx context.Context, pool *pgxpool.Pool, status string) (int, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM organization.organizations WHERE ($1 = '' OR status = $1)`,
		status,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("query.CountOrganizations: %w", err)
	}
	return n, nil
}

// SuspendOrganization sets an organization status to 'suspended' and records the reason.
func SuspendOrganization(ctx context.Context, pool *pgxpool.Pool, organizationID, reason string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE organization.organizations
		SET status = 'suspended', suspended_at = now(), suspended_reason = $2, updated_at = now()
		WHERE id = $1 AND status = 'active'
	`, organizationID, reason)
	if err != nil {
		return fmt.Errorf("query.SuspendOrganization: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("organization %s not found or not active", organizationID)
	}
	return nil
}

// UnsuspendOrganization sets an organization status back to 'active'.
func UnsuspendOrganization(ctx context.Context, pool *pgxpool.Pool, organizationID string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE organization.organizations
		SET status = 'active', suspended_at = NULL, suspended_reason = '', updated_at = now()
		WHERE id = $1 AND status = 'suspended'
	`, organizationID)
	if err != nil {
		return fmt.Errorf("query.UnsuspendOrganization: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("organization %s not found or not suspended", organizationID)
	}
	return nil
}
