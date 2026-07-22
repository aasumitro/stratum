package app

import (
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
)

type SubscriptionStatus struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type PlanCount struct {
	Plan  string `json:"plan"`
	Count int64  `json:"count"`
}

type ProjectMetrics struct {
	TotalOrganizations      int64                `json:"total_organizations"`
	ActiveOrganizations     int64                `json:"active_organizations"`
	TotalMembers            int64                `json:"total_members"`
	SubscriptionsByStatus   []SubscriptionStatus `json:"subscriptions_by_status"`
	MRR                     float64              `json:"mrr"`
	ARR                     float64              `json:"arr"`
	NewOrganizationsLast30d int64                `json:"new_organizations_last_30d"`
	StorageBytesTotal       int64                `json:"storage_bytes_total"`
	TopPlansByOrganization  []PlanCount          `json:"top_plans_by_organization"`
}

type DashboardService struct {
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewDashboardService(projects *ProjectService, pool *connect.PostgresPool) *DashboardService {
	return &DashboardService{projects: projects, pool: pool}
}

func (d *DashboardService) GetMetrics(projectID string) (ProjectMetrics, error) {
	m, err := withProjectDB(d.projects, d.pool, projectID, "DashboardService.GetMetrics", 15*time.Second, query.GetDashboardMetrics)
	if err != nil {
		return ProjectMetrics{}, err
	}

	statuses := make([]SubscriptionStatus, 0, len(m.SubscriptionsByStatus))
	for _, ss := range m.SubscriptionsByStatus {
		statuses = append(statuses, SubscriptionStatus{Status: ss.Status, Count: ss.Count})
	}

	plans := make([]PlanCount, 0, len(m.TopPlansByOrganization))
	for _, pc := range m.TopPlansByOrganization {
		plans = append(plans, PlanCount{Plan: pc.Plan, Count: pc.Count})
	}

	return ProjectMetrics{
		TotalOrganizations:      m.TotalOrganizations,
		ActiveOrganizations:     m.ActiveOrganizations,
		TotalMembers:            m.TotalMembers,
		SubscriptionsByStatus:   statuses,
		MRR:                     m.MRR,
		ARR:                     m.ARR,
		NewOrganizationsLast30d: m.NewOrganizationsLast30d,
		StorageBytesTotal:       m.StorageBytesTotal,
		TopPlansByOrganization:  plans,
	}, nil
}
