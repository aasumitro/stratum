package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditFilter struct {
	Actor          string `json:"actor"`
	Action         string `json:"action"` // HTTP method or empty
	OrganizationID string `json:"organization_id"`
	StatusCode     int    `json:"status_code"` // 0 = no filter
	From           string `json:"from"`        // RFC3339 or empty
	To             string `json:"to"`          // RFC3339 or empty
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
}

type AuditEvent struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Actor          string `json:"actor"`
	Action         string `json:"action"`
	Resource       string `json:"resource"`
	StatusCode     int    `json:"status_code"`
	IP             string `json:"ip"`
	CreatedAt      string `json:"created_at"`
}

type AuditService struct {
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewAuditService(projects *ProjectService, pool *connect.PostgresPool) *AuditService {
	return &AuditService{projects: projects, pool: pool}
}

// ListEvents returns paginated audit events matching the given filter.
func (s *AuditService) ListEvents(projectID string, filter AuditFilter) ([]AuditEvent, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}

	qf, err := buildFilter(filter)
	if err != nil {
		return nil, fmt.Errorf("AuditService.ListEvents: %w", err)
	}

	rows, err := withProjectDB(s.projects, s.pool, projectID, "AuditService.ListEvents", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.AuditRow, error) {
			return query.ListAuditEvents(ctx, db, qf)
		})
	if err != nil {
		return nil, err
	}

	out := make([]AuditEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditEvent{
			ID:             r.ID,
			OrganizationID: r.OrganizationID,
			Actor:          r.Actor,
			Action:         r.Action,
			Resource:       r.Resource,
			StatusCode:     r.StatusCode,
			IP:             r.IP,
			CreatedAt:      r.CreatedAt.Format(time.RFC3339),
		})
	}
	return out, nil
}

// CountEvents returns the total number of matching audit events.
func (s *AuditService) CountEvents(projectID string, filter AuditFilter) (int, error) {
	qf, err := buildFilter(filter)
	if err != nil {
		return 0, fmt.Errorf("AuditService.CountEvents: %w", err)
	}

	return withProjectDB(s.projects, s.pool, projectID, "AuditService.CountEvents", 10*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) (int, error) {
			return query.CountAuditEvents(ctx, db, qf)
		})
}

// ExportCSV returns all matching audit events as a CSV string.
func (s *AuditService) ExportCSV(projectID string, filter AuditFilter) (string, error) {
	filter.Limit = 5000
	filter.Offset = 0

	events, err := s.ListEvents(projectID, filter)
	if err != nil {
		return "", fmt.Errorf("AuditService.ExportCSV: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("id,organization_id,actor,action,resource,status_code,ip,created_at\n")
	for _, e := range events {
		sb.WriteString(csvEscape(e.ID) + "," +
			csvEscape(e.OrganizationID) + "," +
			csvEscape(e.Actor) + "," +
			csvEscape(e.Action) + "," +
			csvEscape(e.Resource) + "," +
			fmt.Sprintf("%d", e.StatusCode) + "," +
			csvEscape(e.IP) + "," +
			csvEscape(e.CreatedAt) + "\n")
	}
	return sb.String(), nil
}

func buildFilter(f AuditFilter) (query.AuditFilter, error) {
	qf := query.AuditFilter{
		Actor:          f.Actor,
		Action:         f.Action,
		OrganizationID: f.OrganizationID,
		StatusCode:     f.StatusCode,
		Limit:          f.Limit,
		Offset:         f.Offset,
	}
	if f.From != "" {
		t, err := time.Parse(time.RFC3339, f.From)
		if err != nil {
			return qf, fmt.Errorf("invalid from date %q: %w", f.From, err)
		}
		qf.From = &t
	}
	if f.To != "" {
		t, err := time.Parse(time.RFC3339, f.To)
		if err != nil {
			return qf, fmt.Errorf("invalid to date %q: %w", f.To, err)
		}
		qf.To = &t
	}
	return qf, nil
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, `",\n`) {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
