package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// attachedAddonRecord is an addon currently attached to a subscription
// (billing.subscription_addons joined with billing.addons), used both by
// the GET /billing/addons list route and invoice composition.
type attachedAddonRecord struct {
	AddonID              string                          `json:"addon_id"`
	Name                 string                          `json:"name"`
	Quantity             int                             `json:"quantity"`
	Prices               map[string]contracts.PlanPrices `json:"prices"`
	ScheduledQuantity    *int                            `json:"scheduled_quantity,omitempty"`
	ScheduledRequestedAt *time.Time                      `json:"scheduled_requested_at,omitempty"`
	// PendingQuantity/PendingInvoiceID mirror ScheduledQuantity/ScheduledRequestedAt's shape but
	// for the opposite direction — an increase awaiting payment, not a decrease deferred to
	// renewal. Set together, cleared together (see ck_subscription_addons_pending_consistent).
	PendingQuantity  *int    `json:"pending_quantity,omitempty"`
	PendingInvoiceID *string `json:"pending_invoice_id,omitempty"`
}

func (r *repository) listAttachedAddonsWithPricing(
	ctx context.Context, q db.Querier, subscriptionID string,
) ([]attachedAddonRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.name, sa.quantity, a.prices, sa.scheduled_quantity, sa.scheduled_requested_at,
		       sa.pending_quantity, sa.pending_invoice_id
		FROM billing.subscription_addons sa
		JOIN billing.addons a ON a.id = sa.addon_id
		WHERE sa.subscription_id = $1
		ORDER BY a.name`, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("billing.listAttachedAddonsWithPricing: %w", err)
	}

	var out []attachedAddonRecord
	for rows.Next() {
		var a attachedAddonRecord
		var pricesJSON []byte
		if err := rows.Scan(&a.AddonID, &a.Name, &a.Quantity, &pricesJSON, &a.ScheduledQuantity, &a.ScheduledRequestedAt,
			&a.PendingQuantity, &a.PendingInvoiceID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("billing.listAttachedAddonsWithPricing: scan: %w", err)
		}
		_ = json.Unmarshal(pricesJSON, &a.Prices)
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listAttachedAddonsWithPricing: %w", err)
	}
	return out, nil
}

// findAttachedAddon looks up one addon row already attached to a
// subscription, live/scheduled/pending quantity all included — used to
// decide whether a requested quantity is an increase or a decrease before
// writing anything. Returns a wrapped pgx.ErrNoRows if the addon isn't
// attached yet.
func (r *repository) findAttachedAddon(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string,
) (*attachedAddonRecord, error) {
	a := &attachedAddonRecord{AddonID: addonID}
	err := q.QueryRow(ctx, `
		SELECT quantity, scheduled_quantity, scheduled_requested_at, pending_quantity, pending_invoice_id
		FROM billing.subscription_addons
		WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID,
	).Scan(&a.Quantity, &a.ScheduledQuantity, &a.ScheduledRequestedAt, &a.PendingQuantity, &a.PendingInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("billing.findAttachedAddon: %w", err)
	}
	return a, nil
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
	if err != nil {
		return fmt.Errorf("billing.upsertSubscriptionAddon: %w", err)
	}
	return nil
}

func (r *repository) deleteSubscriptionAddon(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string,
) error {
	_, err := q.Exec(ctx,
		`DELETE FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID)
	if err != nil {
		return fmt.Errorf("billing.deleteSubscriptionAddon: %w", err)
	}
	return nil
}

// scheduleAddonQuantityChange records a future quantity for an already-
// attached addon without touching its live quantity. quantity = 0 means
// "remove this addon at renewal" — the caller is responsible for ensuring
// the row already exists; this never inserts one.
func (r *repository) scheduleAddonQuantityChange(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string, quantity int,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET scheduled_quantity = $3, scheduled_requested_at = now()
		WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID, quantity)
	if err != nil {
		return fmt.Errorf("billing.scheduleAddonQuantityChange: %w", err)
	}
	return nil
}

// clearScheduledAddonQuantityChange undoes a scheduled quantity change on
// one addon row. The live quantity is untouched — it was never applied.
func (r *repository) clearScheduledAddonQuantityChange(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET scheduled_quantity = NULL, scheduled_requested_at = NULL
		WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID)
	if err != nil {
		return fmt.Errorf("billing.clearScheduledAddonQuantityChange: %w", err)
	}
	return nil
}

// clearAllScheduledAddonQuantityChanges clears every addon's scheduled
// quantity change for a subscription in one statement — used by
// cancellation's supersede-everything-else behavior, which needs to drop an
// unknown number of scheduled addon rows at once rather than looping over
// single-row clears.
func (r *repository) clearAllScheduledAddonQuantityChanges(ctx context.Context, q db.Querier, subscriptionID string) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET scheduled_quantity = NULL, scheduled_requested_at = NULL
		WHERE subscription_id = $1 AND scheduled_quantity IS NOT NULL`,
		subscriptionID)
	if err != nil {
		return fmt.Errorf("billing.clearAllScheduledAddonQuantityChanges: %w", err)
	}
	return nil
}

// scheduledAddonChange is one addon row with a pending quantity change,
// carrying both the live and the future quantity so a caller can compute
// the delta between them without a second query.
type scheduledAddonChange struct {
	AddonID           string
	LiveQuantity      int
	ScheduledQuantity int
}

// listScheduledAddonChanges returns every addon row with something
// scheduled for a subscription — consumed both by the renewal worker
// (applying the change) and by the billing preview (computing the future
// entitlement state before it applies).
func (r *repository) listScheduledAddonChanges(
	ctx context.Context, q db.Querier, subscriptionID string,
) ([]scheduledAddonChange, error) {
	rows, err := q.Query(ctx, `
		SELECT addon_id, quantity, scheduled_quantity
		FROM billing.subscription_addons
		WHERE subscription_id = $1 AND scheduled_quantity IS NOT NULL`,
		subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("billing.listScheduledAddonChanges: %w", err)
	}

	var out []scheduledAddonChange
	for rows.Next() {
		var c scheduledAddonChange
		if err := rows.Scan(&c.AddonID, &c.LiveQuantity, &c.ScheduledQuantity); err != nil {
			rows.Close()
			return nil, fmt.Errorf("billing.listScheduledAddonChanges: scan: %w", err)
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listScheduledAddonChanges: %w", err)
	}
	return out, nil
}

// applyScheduledAddonQuantityChange moves a scheduled quantity into the
// live column and clears the schedule. Only ever called with
// scheduledQuantity > 0 — the "scheduled for removal" (0) case calls
// deleteSubscriptionAddon instead, since this function never deletes a row.
func (r *repository) applyScheduledAddonQuantityChange(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string, scheduledQuantity int,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET quantity = $3, scheduled_quantity = NULL, scheduled_requested_at = NULL
		WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID, scheduledQuantity)
	if err != nil {
		return fmt.Errorf("billing.applyScheduledAddonQuantityChange: %w", err)
	}
	return nil
}

// setPendingAddonIncrease records a target quantity awaiting payment on an
// already-existing addon row (the row must already exist — attachAddon
// inserts a quantity=0 row first for a brand-new attach). The live quantity
// is untouched; it only moves once applyPendingAddonIncrease runs.
func (r *repository) setPendingAddonIncrease(
	ctx context.Context, q db.Querier,
	subscriptionID, addonID string, pendingQuantity int, pendingInvoiceID string,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET pending_quantity = $3, pending_invoice_id = $4
		WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID, pendingQuantity, pendingInvoiceID)
	if err != nil {
		return fmt.Errorf("billing.setPendingAddonIncrease: %w", err)
	}
	return nil
}

// pendingAddonIncrease identifies which subscription/addon row is waiting on
// a given invoice's payment — resolved by handleWebhook (by invoice ID) to
// know which row to fold pending_quantity into once payment is confirmed.
type pendingAddonIncrease struct {
	SubscriptionID string
	AddonID        string
}

// findAddonByPendingInvoice resolves the addon row gated on invoiceID's
// payment. Returns a wrapped pgx.ErrNoRows if no row is currently pending on
// this invoice (e.g. it was already applied or voided).
func (r *repository) findAddonByPendingInvoice(
	ctx context.Context, q db.Querier, invoiceID string,
) (*pendingAddonIncrease, error) {
	p := new(pendingAddonIncrease)
	err := q.QueryRow(ctx, `
		SELECT subscription_id, addon_id
		FROM billing.subscription_addons
		WHERE pending_invoice_id = $1`,
		invoiceID,
	).Scan(&p.SubscriptionID, &p.AddonID)
	if err != nil {
		return nil, fmt.Errorf("billing.findAddonByPendingInvoice: %w", err)
	}
	return p, nil
}

// applyPendingAddonIncrease folds a confirmed-paid pending_quantity into the
// live quantity and clears both pending columns — mirrors
// applyScheduledAddonQuantityChange's shape for the opposite (increase)
// direction.
func (r *repository) applyPendingAddonIncrease(
	ctx context.Context, q db.Querier, subscriptionID, addonID string,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET quantity = pending_quantity, pending_quantity = NULL, pending_invoice_id = NULL
		WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID)
	if err != nil {
		return fmt.Errorf("billing.applyPendingAddonIncrease: %w", err)
	}
	return nil
}

// clearPendingAddonIncrease drops a pending increase without folding it into
// the live quantity — used when a newer request supersedes and voids the
// invoice this row was waiting on.
func (r *repository) clearPendingAddonIncrease(
	ctx context.Context, q db.Querier, subscriptionID, addonID string,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET pending_quantity = NULL, pending_invoice_id = NULL
		WHERE subscription_id = $1 AND addon_id = $2`,
		subscriptionID, addonID)
	if err != nil {
		return fmt.Errorf("billing.clearPendingAddonIncrease: %w", err)
	}
	return nil
}
