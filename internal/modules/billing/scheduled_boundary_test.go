package billing_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

func scheduleDowngradeToSolo(t *testing.T, pool *pgxpool.Pool, user, orgID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	billing.NewModuleEngine(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
	return w
}

func renewalAmounts(t *testing.T, pool *pgxpool.Pool, subID string) map[string]int64 {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT status, amount_cents FROM billing.invoices WHERE subscription_id = $1 AND kind = 'subscription'`, subID)
	if err != nil {
		t.Fatalf("read renewals: %v", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var status string
		var amount int64
		if err := rows.Scan(&status, &amount); err != nil {
			t.Fatalf("scan renewal: %v", err)
		}
		out[status] = amount
	}
	return out
}

// A downgrade scheduled after the renewal was issued must not leave that
// renewal at the old price: paying it would bill the old plan for a whole
// period the owner asked to leave.
func TestScheduledDowngrade_AfterTheRenewalWasIssued_RepricesIt(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_boundary_late_schedule"
		orgID = "00000000-0000-0000-0000-00000000b0a1"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth', cycle = 'monthly' WHERE id = $1`, subID); err != nil {
		t.Fatalf("set growth: %v", err)
	}
	issueRenewal(t, pool, subID, orgID)
	before := renewalAmounts(t, pool, subID)["pending"]

	useProviderTransport(t, http.StatusInternalServerError)
	if w := scheduleDowngradeToSolo(t, pool, user, orgID); w.Code != http.StatusOK {
		t.Fatalf("downgrade: want 200, got %d: %s", w.Code, w.Body)
	}

	after := renewalAmounts(t, pool, subID)
	if after["void"] != before {
		t.Errorf("the renewal priced at growth: want it void, got %v", after)
	}
	if after["pending"] == 0 || after["pending"] >= before {
		t.Errorf("want a pending renewal priced at solo (below %d), got %v", before, after)
	}
	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Errorf("status: want active, got %q", got)
	}
	var plan string
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	if plan != "growth" {
		t.Errorf("plan: want growth until the boundary, got %q", plan)
	}
}

// Once the renewal priced from the schedule is paid, the owner has paid the
// lower price for the coming period: undoing the downgrade then would keep the
// higher plan at that price.
func TestScheduledDowngrade_UndoAfterThePricedRenewalIsPaid_IsRefused(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_boundary_undo_locked"
		orgID = "00000000-0000-0000-0000-00000000b0a2"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth', cycle = 'monthly' WHERE id = $1`, subID); err != nil {
		t.Fatalf("set growth: %v", err)
	}
	if w := scheduleDowngradeToSolo(t, pool, user, orgID); w.Code != http.StatusOK {
		t.Fatalf("downgrade: want 200, got %d: %s", w.Code, w.Body)
	}
	// The renewal priced from it was paid: period_end moved past the boundary.
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET period_end = period_end + interval '1 month' WHERE id = $1`, subID); err != nil {
		t.Fatalf("pay renewal: %v", err)
	}

	w := httptest.NewRecorder()
	billing.NewModuleEngine(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade/undo", ""))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "AMENDMENT_ALREADY_PAID") {
		t.Errorf("undo: want 422 AMENDMENT_ALREADY_PAID, got %d: %s", w.Code, w.Body)
	}
}

// An ended subscription has no period running, so a downgrade applies at once
// and a Resume is priced at the new plan.
func TestDowngrade_EndedSubscription_AppliesNow(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_boundary_ended_downgrade"
		orgID = "00000000-0000-0000-0000-00000000b0a3"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth', cycle = 'monthly', status = 'expired' WHERE id = $1`, subID); err != nil {
		t.Fatalf("set expired growth: %v", err)
	}
	resume := seedInvoice(pool, subID, "USD", 2900)

	if w := scheduleDowngradeToSolo(t, pool, user, orgID); w.Code != http.StatusOK {
		t.Fatalf("downgrade: want 200, got %d: %s", w.Code, w.Body)
	}
	var plan string
	var scheduled *string
	pool.QueryRow(t.Context(), `SELECT plan, scheduled_plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan, &scheduled)
	if plan != "solo" || scheduled != nil {
		t.Errorf("want plan solo applied with nothing scheduled, got plan=%q scheduled=%v", plan, scheduled)
	}
	if got := invoiceStatus(t, pool, resume); got != "void" {
		t.Errorf("Resume invoice priced at growth: want void, got %q", got)
	}
}

// A plan change replaces a scheduled downgrade, which would otherwise undo it at
// the boundary.
func TestPlanChange_ClearsAScheduledDowngrade(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_boundary_change_clears"
		orgID = "00000000-0000-0000-0000-00000000b0a4"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth', cycle = 'monthly' WHERE id = $1`, subID); err != nil {
		t.Fatalf("set growth: %v", err)
	}
	if w := scheduleDowngradeToSolo(t, pool, user, orgID); w.Code != http.StatusOK {
		t.Fatalf("downgrade: want 200, got %d: %s", w.Code, w.Body)
	}

	useProviderTransport(t, http.StatusInternalServerError)
	w := httptest.NewRecorder()
	billing.NewModuleEngine(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"growth","cycle":"yearly","terms_agreed":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("plan change: want 200, got %d: %s", w.Code, w.Body)
	}
	var scheduled *string
	var effective *time.Time
	pool.QueryRow(t.Context(), `SELECT scheduled_plan, scheduled_effective_at FROM billing.subscriptions WHERE id = $1`, subID).Scan(&scheduled, &effective)
	if scheduled != nil || effective != nil {
		t.Errorf("want the scheduled downgrade cleared by the plan change, got scheduled=%v effective=%v", scheduled, effective)
	}
}

// A subscription that expired at the boundary still takes the schedule, so a
// later Resume is priced and runs on the plan the owner chose.
func TestApplyScheduled_ExpiredAtTheBoundary_Applies(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_boundary_expired_applies"
		orgID = "00000000-0000-0000-0000-00000000b0a5"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	var effective time.Time
	if err := pool.QueryRow(t.Context(), `
		UPDATE billing.subscriptions
		SET plan = 'growth', cycle = 'monthly', status = 'expired',
		    scheduled_plan = 'solo', scheduled_cycle = 'monthly', scheduled_requested_at = now(),
		    period_end = now() - interval '1 minute', scheduled_effective_at = now() - interval '1 minute'
		WHERE id = $1 RETURNING scheduled_effective_at`, subID).Scan(&effective); err != nil {
		t.Fatalf("set expired with schedule: %v", err)
	}

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleApplyScheduled(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, effective, false)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var plan string
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	if plan != "solo" {
		t.Errorf("plan: want solo, got %q", plan)
	}

	// An unpaid renewal at the boundary of a live subscription is left to the expiry.
	if _, err := pool.Exec(t.Context(), `
		UPDATE billing.subscriptions
		SET plan = 'growth', status = 'active', scheduled_plan = 'solo', scheduled_cycle = 'monthly',
		    scheduled_requested_at = now(), period_end = $2, scheduled_effective_at = $2
		WHERE id = $1`, subID, effective); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := mod.Worker.HandleApplyScheduled(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, effective, false)); err != nil {
		t.Fatalf("apply unpaid: %v", err)
	}
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	if plan != "growth" {
		t.Errorf("unpaid renewal: want nothing applied before the expiry, got plan %q", plan)
	}
}
