package query

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SubStatus struct {
	Status string
	Count  int64
}

type PlanCount struct {
	Plan  string
	Count int64
}

type DashboardMetrics struct {
	TotalOrganizations      int64
	ActiveOrganizations     int64
	TotalMembers            int64
	SubscriptionsByStatus   []SubStatus
	MRR                     float64
	ARR                     float64
	NewOrganizationsLast30d int64
	StorageBytesTotal       int64
	TopPlansByOrganization  []PlanCount
}

func GetDashboardMetrics(ctx context.Context, pool *pgxpool.Pool) (*DashboardMetrics, error) {
	m := &DashboardMetrics{}

	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'active')
		FROM organization.organizations
		WHERE status != 'deleted'
	`).Scan(&m.TotalOrganizations, &m.ActiveOrganizations); err != nil {
		return nil, fmt.Errorf("query.dashboard: organizations: %w", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT auth_sub) FROM organization.memberships
	`).Scan(&m.TotalMembers); err != nil {
		return nil, fmt.Errorf("query.dashboard: members: %w", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT status, COUNT(*) FROM billing.subscriptions GROUP BY status ORDER BY status
	`)
	if err != nil {
		return nil, fmt.Errorf("query.dashboard: subscriptions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ss SubStatus
		if err := rows.Scan(&ss.Status, &ss.Count); err != nil {
			return nil, fmt.Errorf("query.dashboard: subscriptions scan: %w", err)
		}
		m.SubscriptionsByStatus = append(m.SubscriptionsByStatus, ss)
	}
	rows.Close()

	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) / 100.0
		FROM billing.invoices
		WHERE status = 'paid'
		  AND created_at >= date_trunc('month', now())
	`).Scan(&m.MRR); err != nil {
		return nil, fmt.Errorf("query.dashboard: mrr: %w", err)
	}
	m.ARR = m.MRR * 12

	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM organization.organizations
		WHERE created_at >= now() - INTERVAL '30 days'
	`).Scan(&m.NewOrganizationsLast30d); err != nil {
		return nil, fmt.Errorf("query.dashboard: new_organizations: %w", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(value), 0)
		FROM billing.usage
		WHERE metric = 'storage_bytes'
	`).Scan(&m.StorageBytesTotal); err != nil {
		return nil, fmt.Errorf("query.dashboard: storage: %w", err)
	}

	planRows, err := pool.Query(ctx, `
		SELECT p.name, COUNT(s.id) AS cnt
		FROM billing.subscriptions s
		JOIN billing.plans p ON p.id = s.plan
		WHERE s.status IN ('active', 'trialing')
		GROUP BY p.name
		ORDER BY cnt DESC
		LIMIT 5
	`)
	if err != nil {
		return nil, fmt.Errorf("query.dashboard: top_plans: %w", err)
	}
	defer planRows.Close()
	for planRows.Next() {
		var pc PlanCount
		if err := planRows.Scan(&pc.Plan, &pc.Count); err != nil {
			return nil, fmt.Errorf("query.dashboard: top_plans scan: %w", err)
		}
		m.TopPlansByOrganization = append(m.TopPlansByOrganization, pc)
	}

	return m, nil
}
