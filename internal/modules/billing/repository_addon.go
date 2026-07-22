package billing

import (
	"context"
	"encoding/json"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// attachedAddonRecord is an addon currently attached to a subscription
// (billing.subscription_addons joined with billing.addons), used both by
// the GET /billing/addons list route and invoice composition.
type attachedAddonRecord struct {
	AddonID  string                          `json:"addon_id"`
	Name     string                          `json:"name"`
	Quantity int                             `json:"quantity"`
	Prices   map[string]contracts.PlanPrices `json:"prices"`
}

func (r *repository) listAttachedAddonsWithPricing(
	ctx context.Context, q db.Querier, subscriptionID string,
) ([]attachedAddonRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.name, sa.quantity, a.prices
		FROM billing.subscription_addons sa
		JOIN billing.addons a ON a.id = sa.addon_id
		WHERE sa.subscription_id = $1
		ORDER BY a.name`, subscriptionID)
	if err != nil {
		return nil, err
	}

	var out []attachedAddonRecord
	for rows.Next() {
		var a attachedAddonRecord
		var pricesJSON []byte
		if err := rows.Scan(&a.AddonID, &a.Name, &a.Quantity, &pricesJSON); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(pricesJSON, &a.Prices)
		out = append(out, a)
	}
	rows.Close()
	return out, rows.Err()
}

func (r *repository) upsertSubscriptionAddon(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string, quantity int,
) error {
	_, err := q.Exec(ctx, `
		INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity)
		VALUES ($1, $2, $3)
		ON CONFLICT (subscription_id, addon_id) DO UPDATE SET quantity = $3`,
		subscriptionID, addonID, quantity)
	return err
}

func (r *repository) deleteSubscriptionAddon(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string,
) error {
	_, err := q.Exec(ctx,
		`DELETE FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID)
	return err
}
