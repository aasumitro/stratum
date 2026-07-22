package app

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrganizationSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Status        string `json:"status"`
	CountryCode   string `json:"country_code"`
	OwnerID       string `json:"owner_id"`
	CreatedAt     string `json:"created_at"`
	MemberCount   int    `json:"member_count"`
	PlanName      string `json:"plan_name"`
	BillingStatus string `json:"billing_status"`
}

type OrganizationOpsService struct {
	db       *sql.DB
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewOrganizationOpsService(db *sql.DB, projects *ProjectService, pool *connect.PostgresPool) *OrganizationOpsService {
	return &OrganizationOpsService{db: db, projects: projects, pool: pool}
}

// ListOrganizations returns organizations for the target project, optionally filtered by status.
// status: "" = all (excluding deleted), "active" | "suspended" | "deleted"
// limit/offset enable pagination.
func (s *OrganizationOpsService) ListOrganizations(projectID, status string, limit, offset int) ([]OrganizationSummary, error) {
	rows, err := withProjectDB(s.projects, s.pool, projectID, "OrganizationOpsService.ListOrganizations", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.OrganizationRow, error) {
			return query.ListOrganizations(ctx, db, status, limit, offset)
		})
	if err != nil {
		return nil, err
	}

	out := make([]OrganizationSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, OrganizationSummary{
			ID:            r.ID,
			Name:          r.Name,
			Slug:          r.Slug,
			Status:        r.Status,
			CountryCode:   r.CountryCode,
			OwnerID:       r.OwnerID,
			CreatedAt:     r.CreatedAt.Format(time.RFC3339),
			MemberCount:   r.MemberCount,
			PlanName:      r.PlanName,
			BillingStatus: r.BillingStatus,
		})
	}
	return out, nil
}

// CountOrganizations returns the total number of organizations for the given status filter.
func (s *OrganizationOpsService) CountOrganizations(projectID, status string) (int, error) {
	return withProjectDB(s.projects, s.pool, projectID, "OrganizationOpsService.CountOrganizations", 10*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) (int, error) {
			return query.CountOrganizations(ctx, db, status)
		})
}

// SuspendOrganization suspends an active organization and records the reason.
func (s *OrganizationOpsService) SuspendOrganization(projectID, organizationID, reason string) error {
	err := withProjectDBErr(s.projects, s.pool, projectID, "OrganizationOpsService.SuspendOrganization", 10*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.SuspendOrganization(ctx, db, organizationID, reason)
		})
	if err != nil {
		return err
	}

	s.logAction(projectID, "suspend_organization", organizationID, reason)
	return nil
}

// UnsuspendOrganization restores a suspended organization to active status.
func (s *OrganizationOpsService) UnsuspendOrganization(projectID, organizationID string) error {
	err := withProjectDBErr(s.projects, s.pool, projectID, "OrganizationOpsService.UnsuspendOrganization", 10*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UnsuspendOrganization(ctx, db, organizationID)
		})
	if err != nil {
		return err
	}

	s.logAction(projectID, "unsuspend_organization", organizationID, "")
	return nil
}

func (s *OrganizationOpsService) logAction(projectID, action, targetID, detail string) {
	_, err := s.db.ExecContext(context.Background(), `
		INSERT INTO operator_log (project_id, action, target_id, detail)
		VALUES (?, ?, ?, ?)
	`, projectID, action, targetID, detail)
	if err != nil {
		log.Printf("OrganizationOpsService.logAction: %v", err)
	}
}
