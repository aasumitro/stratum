package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserResult struct {
	AuthSub    string  `json:"auth_sub"`
	FullName   string  `json:"full_name"`
	Email      string  `json:"email"`
	AvatarURL  string  `json:"avatar_url"`
	LastSeenAt *string `json:"last_seen_at"`
}

type UserOrganization struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Slug          string  `json:"slug"`
	Status        string  `json:"status"`
	Role          string  `json:"role"`
	Plan          string  `json:"plan"`
	PlanName      string  `json:"plan_name"`
	BillingStatus string  `json:"billing_status"`
	TrialEnd      *string `json:"trial_end"`
	PeriodEnd     *string `json:"period_end"`
}

type UserInvoice struct {
	ID          string  `json:"id"`
	AmountCents int64   `json:"amount_cents"`
	Currency    string  `json:"currency"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	PaidAt      *string `json:"paid_at"`
}

type UserDetail struct {
	AuthSub       string             `json:"auth_sub"`
	FullName      string             `json:"full_name"`
	Email         string             `json:"email"`
	AvatarURL     string             `json:"avatar_url"`
	LastSeenAt    *string            `json:"last_seen_at"`
	Organizations []UserOrganization `json:"organizations"`
}

type UserLoginEvent struct {
	IPAddress string `json:"ip_address"`
	UserAgent string `json:"user_agent"`
	CreatedAt string `json:"created_at"`
}

type AtRiskInvoice struct {
	InvoiceID        string `json:"invoice_id"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	OrganizationSlug string `json:"organization_slug"`
	AmountCents      int64  `json:"amount_cents"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
	DaysPending      int    `json:"days_pending"`
}

type SupportService struct {
	db       *sql.DB
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewSupportService(db *sql.DB, projects *ProjectService, pool *connect.PostgresPool) *SupportService {
	return &SupportService{db: db, projects: projects, pool: pool}
}

func (s *SupportService) SearchUsers(projectID, q string) ([]UserResult, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "SupportService.SearchUsers", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.SupportUser, error) {
			return query.SearchSupportUsers(ctx, db, q)
		})
	if err != nil {
		return nil, err
	}

	out := make([]UserResult, len(raw))
	for i, u := range raw {
		out[i] = UserResult{
			AuthSub:   u.AuthSub,
			FullName:  u.FullName,
			Email:     u.Email,
			AvatarURL: u.AvatarURL,
		}
		if u.LastSeenAt != nil {
			ts := u.LastSeenAt.Format(time.RFC3339)
			out[i].LastSeenAt = &ts
		}
	}
	return out, nil
}

func (s *SupportService) GetUserDetail(projectID, authSub string) (UserDetail, error) {
	detail, err := withProjectDB(s.projects, s.pool, projectID, "SupportService.GetUserDetail", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) (UserDetail, error) {
			users, err := query.SearchSupportUsers(ctx, db, authSub)
			if err != nil {
				return UserDetail{}, err
			}
			if len(users) == 0 {
				return UserDetail{}, fmt.Errorf("user not found")
			}
			u := users[0]

			organizations, err := query.GetSupportUserOrganizations(ctx, db, authSub)
			if err != nil {
				return UserDetail{}, err
			}

			detail := UserDetail{
				AuthSub:       u.AuthSub,
				FullName:      u.FullName,
				Email:         u.Email,
				AvatarURL:     u.AvatarURL,
				Organizations: make([]UserOrganization, len(organizations)),
			}
			if u.LastSeenAt != nil {
				ts := u.LastSeenAt.Format(time.RFC3339)
				detail.LastSeenAt = &ts
			}
			for i, o := range organizations {
				detail.Organizations[i] = UserOrganization{
					ID:            o.ID,
					Name:          o.Name,
					Slug:          o.Slug,
					Status:        o.Status,
					Role:          o.Role,
					Plan:          o.Plan,
					PlanName:      o.PlanName,
					BillingStatus: o.BillingStatus,
				}
				if o.TrialEnd != nil {
					ts := o.TrialEnd.Format(time.RFC3339)
					detail.Organizations[i].TrialEnd = &ts
				}
				if o.PeriodEnd != nil {
					ts := o.PeriodEnd.Format(time.RFC3339)
					detail.Organizations[i].PeriodEnd = &ts
				}
			}
			return detail, nil
		})
	if err != nil {
		return UserDetail{}, err
	}
	return detail, nil
}

// GetUserLoginHistory returns a user's 20 most recent login events, newest first.
func (s *SupportService) GetUserLoginHistory(projectID, authSub string) ([]UserLoginEvent, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "SupportService.GetUserLoginHistory", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.SupportLoginEvent, error) {
			return query.ListSupportUserLoginEvents(ctx, db, authSub, 20)
		})
	if err != nil {
		return nil, err
	}

	out := make([]UserLoginEvent, len(raw))
	for i, e := range raw {
		out[i] = UserLoginEvent{
			IPAddress: e.IPAddress,
			UserAgent: e.UserAgent,
			CreatedAt: e.CreatedAt.Format(time.RFC3339),
		}
	}
	return out, nil
}

func (s *SupportService) GetOrganizationInvoices(projectID, organizationID string) ([]UserInvoice, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "SupportService.GetOrganizationInvoices", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.SupportInvoice, error) {
			return query.ListSupportOrganizationInvoices(ctx, db, organizationID)
		})
	if err != nil {
		return nil, err
	}

	out := make([]UserInvoice, len(raw))
	for i, inv := range raw {
		out[i] = UserInvoice{
			ID:          inv.ID,
			AmountCents: inv.AmountCents,
			Currency:    inv.Currency,
			Status:      inv.Status,
			CreatedAt:   inv.CreatedAt.Format(time.RFC3339),
		}
		if inv.PaidAt != nil {
			ts := inv.PaidAt.Format(time.RFC3339)
			out[i].PaidAt = &ts
		}
	}
	return out, nil
}

func (s *SupportService) ExtendTrial(projectID, organizationID string, days int) error {
	if days <= 0 {
		return fmt.Errorf("SupportService.ExtendTrial: days must be positive")
	}

	err := withProjectDBErr(s.projects, s.pool, projectID, "SupportService.ExtendTrial", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.SupportExtendTrial(ctx, db, organizationID, days)
		})
	if err != nil {
		return err
	}
	s.logAction(projectID, "extend_trial", organizationID, fmt.Sprintf("+%d days", days))
	return nil
}

func (s *SupportService) ActivateSubscription(projectID, organizationID string) error {
	err := withProjectDBErr(s.projects, s.pool, projectID, "SupportService.ActivateSubscription", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.SupportActivateSubscription(ctx, db, organizationID)
		})
	if err != nil {
		return err
	}
	s.logAction(projectID, "activate_subscription", organizationID, "")
	return nil
}

func (s *SupportService) ChangePlan(projectID, organizationID, plan, cycle string) error {
	if plan == "" {
		return fmt.Errorf("SupportService.ChangePlan: plan is required")
	}
	if cycle != "monthly" && cycle != "yearly" {
		return fmt.Errorf("SupportService.ChangePlan: cycle must be monthly or yearly")
	}

	err := withProjectDBErr(s.projects, s.pool, projectID, "SupportService.ChangePlan", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.SupportChangePlan(ctx, db, organizationID, plan, cycle)
		})
	if err != nil {
		return err
	}
	s.logAction(projectID, "change_plan", organizationID, fmt.Sprintf("%s/%s", plan, cycle))
	return nil
}

func (s *SupportService) MarkInvoicePaid(projectID, invoiceID string) error {
	err := withProjectDBErr(s.projects, s.pool, projectID, "SupportService.MarkInvoicePaid", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.SupportMarkInvoicePaid(ctx, db, invoiceID)
		})
	if err != nil {
		return err
	}
	s.logAction(projectID, "mark_invoice_paid", invoiceID, "")
	return nil
}

func (s *SupportService) VoidInvoice(projectID, invoiceID string) error {
	err := withProjectDBErr(s.projects, s.pool, projectID, "SupportService.VoidInvoice", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.SupportVoidInvoice(ctx, db, invoiceID)
		})
	if err != nil {
		return err
	}
	s.logAction(projectID, "void_invoice", invoiceID, "")
	return nil
}

func (s *SupportService) GetAtRiskInvoices(projectID string) ([]AtRiskInvoice, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "SupportService.GetAtRiskInvoices", 15*time.Second, query.GetAtRiskInvoices)
	if err != nil {
		return nil, err
	}

	out := make([]AtRiskInvoice, len(raw))
	for i, inv := range raw {
		out[i] = AtRiskInvoice{
			InvoiceID:        inv.InvoiceID,
			OrganizationID:   inv.OrganizationID,
			OrganizationName: inv.OrganizationName,
			OrganizationSlug: inv.OrganizationSlug,
			AmountCents:      inv.AmountCents,
			Currency:         inv.Currency,
			Status:           inv.Status,
			CreatedAt:        inv.CreatedAt.Format(time.RFC3339),
			DaysPending:      inv.DaysPending,
		}
	}
	return out, nil
}

func (s *SupportService) logAction(projectID, action, targetID, detail string) {
	_, err := s.db.Exec(
		`INSERT INTO operator_log (project_id, action, target_id, detail) VALUES (?, ?, ?, ?)`,
		projectID, action, targetID, detail,
	)
	if err != nil {
		log.Printf("SupportService.logAction: %v", err)
	}
}
