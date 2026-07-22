package app

import (
	"context"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WatchlistItem struct {
	OrganizationID   string  `json:"organization_id"`
	OrganizationName string  `json:"organization_name"`
	OrganizationSlug string  `json:"organization_slug"`
	PlanName         string  `json:"plan_name"`
	Status           string  `json:"status"`
	TrialEnd         *string `json:"trial_end"`
	PeriodEnd        *string `json:"period_end"`
	DaysRemaining    int     `json:"days_remaining"`
}

type WatchlistResult struct {
	TrialsEndingSoon     []WatchlistItem `json:"trials_ending_soon"`
	PastDue              []WatchlistItem `json:"past_due"`
	RenewalsDue          []WatchlistItem `json:"renewals_due"`
	CancellationsPending []WatchlistItem `json:"cancellations_pending"`
}

type WatchlistService struct {
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewWatchlistService(projects *ProjectService, pool *connect.PostgresPool) *WatchlistService {
	return &WatchlistService{projects: projects, pool: pool}
}

// GetAll returns all four watchlist buckets in a single call.
func (s *WatchlistService) GetAll(projectID string) (WatchlistResult, error) {
	return withProjectDB(s.projects, s.pool, projectID, "WatchlistService.GetAll", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) (WatchlistResult, error) {
			trials, err := query.GetTrialsEndingSoon(ctx, db)
			if err != nil {
				return WatchlistResult{}, err
			}

			pastDue, err := query.GetPastDue(ctx, db)
			if err != nil {
				return WatchlistResult{}, err
			}

			renewals, err := query.GetRenewalsDue(ctx, db)
			if err != nil {
				return WatchlistResult{}, err
			}

			cancellations, err := query.GetCancellationsTakingEffect(ctx, db)
			if err != nil {
				return WatchlistResult{}, err
			}

			return WatchlistResult{
				TrialsEndingSoon:     mapWatchlistItems(trials),
				PastDue:              mapWatchlistItems(pastDue),
				RenewalsDue:          mapWatchlistItems(renewals),
				CancellationsPending: mapWatchlistItems(cancellations),
			}, nil
		})
}

func mapWatchlistItems(raw []query.WatchlistItem) []WatchlistItem {
	if len(raw) == 0 {
		return []WatchlistItem{}
	}
	out := make([]WatchlistItem, len(raw))
	for i, r := range raw {
		out[i] = WatchlistItem{
			OrganizationID:   r.OrganizationID,
			OrganizationName: r.OrganizationName,
			OrganizationSlug: r.OrganizationSlug,
			PlanName:         r.PlanName,
			Status:           r.Status,
			DaysRemaining:    r.DaysRemaining,
		}
		if r.TrialEnd != nil {
			ts := r.TrialEnd.Format(time.RFC3339)
			out[i].TrialEnd = &ts
		}
		if r.PeriodEnd != nil {
			ts := r.PeriodEnd.Format(time.RFC3339)
			out[i].PeriodEnd = &ts
		}
	}
	return out
}
