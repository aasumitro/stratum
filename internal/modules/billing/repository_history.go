package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type historyRecord struct {
	ID             string    `json:"id"`
	SubscriptionID string    `json:"subscription_id"`
	Action         string    `json:"action"`
	FromPlan       *string   `json:"from_plan,omitempty"`
	ToPlan         *string   `json:"to_plan,omitempty"`
	AmountCents    int64     `json:"amount_cents"`
	Currency       string    `json:"currency"`
	ChangedBy      string    `json:"changed_by"`
	ChangedAt      time.Time `json:"changed_at"`
	// json.RawMessage (not []byte) so encoding/json embeds the jsonb
	// column's content verbatim instead of base64-encoding it; the
	// swaggertype override is needed because swag still resolves the
	// underlying []byte and would otherwise document this as an integer
	// array instead of an object.
	Metadata    json.RawMessage `json:"metadata,omitempty" swaggertype:"object"`
	Phase       *string         `json:"phase,omitempty"`
	EffectiveAt *time.Time      `json:"effective_at,omitempty"`
	FromCycle   *string         `json:"from_cycle,omitempty"`
	ToCycle     *string         `json:"to_cycle,omitempty"`
}

func (r *repository) insertHistory(
	ctx context.Context, q db.Querier,
	subscriptionID, action string,
	fromPlan, toPlan *string,
	amountCents int64, currency, changedBy string,
	metadata []byte,
) (string, error) {
	return r.insertHistoryWithPhase(ctx, q, subscriptionID, action, fromPlan, toPlan,
		amountCents, currency, changedBy, metadata, nil, nil)
}

// insertHistoryWithPhase is insertHistory plus phase/effective_at — a
// scheduled amendment writes phase='scheduled' when first requested, a
// second row with phase='applied' when the renewal worker applies it, and
// (undo) a third row with phase='undone' if it's cleared before that happens;
// every other action leaves both NULL, same as from_plan/to_plan already do
// for actions they don't apply to.
func (r *repository) insertHistoryWithPhase(
	ctx context.Context, q db.Querier,
	subscriptionID, action string,
	fromPlan, toPlan *string,
	amountCents int64, currency, changedBy string,
	metadata []byte,
	phase *string, effectiveAt *time.Time,
) (string, error) {
	return r.insertHistoryWithCycle(ctx, q, subscriptionID, action, fromPlan, toPlan,
		amountCents, currency, changedBy, metadata, phase, effectiveAt, nil, nil)
}

// insertHistoryWithCycle is insertHistoryWithPhase plus from_cycle/to_cycle
// — only the plan/cycle-changing call sites (change-plan, scheduled
// downgrade, renewal-applied downgrade) have a cycle to report; every other
// caller goes through insertHistory/insertHistoryWithPhase, which pass nil.
func (r *repository) insertHistoryWithCycle(
	ctx context.Context, q db.Querier,
	subscriptionID, action string,
	fromPlan, toPlan *string,
	amountCents int64, currency, changedBy string,
	metadata []byte,
	phase *string, effectiveAt *time.Time,
	fromCycle, toCycle *string,
) (string, error) {
	if metadata == nil {
		metadata = []byte("{}")
	}
	var id string
	err := q.QueryRow(ctx, `
		INSERT INTO billing.subscription_history
			(subscription_id, action, from_plan, to_plan, amount_cents, currency, changed_by, metadata, phase, effective_at, from_cycle, to_cycle)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`,
		subscriptionID, action, fromPlan, toPlan, amountCents, currency, changedBy, metadata, phase, effectiveAt, fromCycle, toCycle,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("billing.insertHistoryWithCycle: %w", err)
	}
	return id, nil
}

func (r *repository) updateHistoryMetadata(ctx context.Context, q db.Querier, historyID string, metadata []byte) error {
	_, err := q.Exec(ctx, `UPDATE billing.subscription_history SET metadata = $2 WHERE id = $1`, historyID, metadata)
	if err != nil {
		return fmt.Errorf("billing.updateHistoryMetadata: %w", err)
	}
	return nil
}

// anonymizeHistory replaces changed_by with a placeholder across a user's
// subscription history entries — used by the GDPR account-deletion flow.
func (r *repository) anonymizeHistory(ctx context.Context, q db.Querier, authSub string) error {
	_, err := q.Exec(ctx, `UPDATE billing.subscription_history SET changed_by = 'deleted_user' WHERE changed_by = $1`, authSub)
	if err != nil {
		return fmt.Errorf("billing.anonymizeHistory: %w", err)
	}
	return nil
}

func (r *repository) listHistory(
	ctx context.Context, q db.Querier,
	subscriptionID string,
) ([]historyRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, subscription_id, action, from_plan, to_plan,
		       amount_cents, currency, changed_by, changed_at, metadata,
		       phase, effective_at, from_cycle, to_cycle
		FROM billing.subscription_history
		WHERE subscription_id = $1
		ORDER BY changed_at DESC`,
		subscriptionID,
	)
	if err != nil {
		return nil, fmt.Errorf("billing.listHistory: %w", err)
	}
	defer rows.Close()

	var out []historyRecord
	for rows.Next() {
		var h historyRecord
		if err := rows.Scan(
			&h.ID, &h.SubscriptionID, &h.Action, &h.FromPlan, &h.ToPlan,
			&h.AmountCents, &h.Currency, &h.ChangedBy, &h.ChangedAt, &h.Metadata,
			&h.Phase, &h.EffectiveAt, &h.FromCycle, &h.ToCycle,
		); err != nil {
			return nil, fmt.Errorf("billing.listHistory: scan: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listHistory: %w", err)
	}
	return out, nil
}
