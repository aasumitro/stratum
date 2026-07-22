package notification_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/mailer"
)

// deadMailer is configured (host set) but points at a closed port, so
// Send fails fast — the template still renders and the email row is recorded
// with status "failed", exercising sendEmail end to end without real SMTP.
func deadMailer() *mailer.Mailer {
	return mailer.New(config.SMTPConfig{Host: "127.0.0.1", Port: 1, Username: "noreply@test.local", FromName: "Test"})
}

// encodeEnvNotif builds a marshalled event envelope the worker handlers decode.
func encodeEnvNotif(id, routingKey, orgID string, data any) []byte {
	env := events.Envelope{
		ID: id, Type: routingKey, Source: "test", Time: time.Now(), OrgID: orgID, Data: data,
	}
	b, _ := json.Marshal(env)
	return b
}

// assertNotifChannelExists asserts at least one message row exists for the
// given organization + channel.
func assertNotifChannelExists(t *testing.T, pool *pgxpool.Pool, orgID, channel string) {
	t.Helper()
	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = $2`, orgID, channel,
	).Scan(&count)
	if count == 0 {
		t.Errorf("expected at least one %q notification for organization %s", channel, orgID)
	}
}

// notifMembersReader resolves a fixed member list + organization name, so the
// organization-wide fan-out handlers (suspend/reactivate/delete) have real
// recipients. Embeds markReadOwnerReader for the rest of the interface.
type notifMembersReader struct {
	markReadOwnerReader
	members []string
	name    string
}

func (r notifMembersReader) GetOrganizationByID(_ context.Context, _ string) (*contracts.OrganizationInfo, error) {
	return &contracts.OrganizationInfo{OwnerID: testAuthSubNotif, Name: r.name}, nil
}

func (r notifMembersReader) ListMemberAuthSubs(_ context.Context, _ string) ([]string, error) {
	return r.members, nil
}

// --- organization-wide fan-out handlers ---

func TestIntegration_HandleOrganizationSuspended_NotifiesEachMember(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f01"
	members := []string{"integ_sub_notif_m1", "integ_sub_notif_m2"}
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{members: members, name: "Acme"}, nil, "", nil)
	body := encodeEnvNotif("sus-01", events.RoutingKeyOrganizationSuspended, orgID,
		events.OrganizationSuspended{OrganizationID: orgID, Reason: "unpaid invoice", SuspendedAt: time.Now()})

	if err := mod.Worker.HandleOrganizationSuspended(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationSuspended: %v", err)
	}

	var count int
	var sampleBody string
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*), COALESCE(MAX(body),'') FROM notification.messages WHERE organization_id = $1 AND channel = 'organization_suspended'`, orgID,
	).Scan(&count, &sampleBody)
	if count != len(members) {
		t.Errorf("want one suspended notification per member (%d), got %d", len(members), count)
	}
	if !strings.Contains(sampleBody, "unpaid invoice") {
		t.Errorf("suspend notification should carry the reason, got %q", sampleBody)
	}
}

func TestIntegration_HandleOrganizationReactivated_NotifiesEachMember(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f02"
	members := []string{"integ_sub_notif_r1"}
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{members: members, name: "Acme"}, nil, "", nil)
	body := encodeEnvNotif("react-01", events.RoutingKeyOrganizationReactivated, orgID,
		events.OrganizationReactivated{OrganizationID: orgID, ReactivatedAt: time.Now()})

	if err := mod.Worker.HandleOrganizationReactivated(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationReactivated: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "organization_reactivated")
}

func TestIntegration_HandleOrganizationDeleted_NotifiesEachMemberWithName(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f03"
	members := []string{"integ_sub_notif_d1", "integ_sub_notif_d2"}
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{members: members, name: "Globex Inc"}, nil, "", nil)
	body := encodeEnvNotif("del-01", events.RoutingKeyOrganizationDeleted, orgID,
		events.OrganizationDeleted{OrganizationID: orgID, DeletedAt: time.Now()})

	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationDeleted: %v", err)
	}

	var count int
	var sampleBody string
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*), COALESCE(MAX(body),'') FROM notification.messages WHERE organization_id = $1 AND channel = 'organization_deleted'`, orgID,
	).Scan(&count, &sampleBody)
	if count != len(members) {
		t.Errorf("want %d deleted notifications, got %d", len(members), count)
	}
	if !strings.Contains(sampleBody, "Globex Inc") {
		t.Errorf("deleted notification should carry the resolved organization name, got %q", sampleBody)
	}
}

// TestIntegration_HandleOrganizationSuspended_SkipsMemberWithDisabledPreference
// guards the batched fan-out (service.sendToMany / repository.listDisabledAuthSubs)
// added to fix the N+1 preference-query-per-member pattern: a mixed batch of
// enabled and explicitly-disabled recipients must still resolve correctly in
// one query, not fail open (send to everyone) or closed (skip everyone).
func TestIntegration_HandleOrganizationSuspended_SkipsMemberWithDisabledPreference(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f05"
	members := []string{"integ_sub_notif_pref1", "integ_sub_notif_pref2", "integ_sub_notif_pref3"}
	optedOut := members[1]
	t.Cleanup(func() {
		cleanupNotifByOrganization(pool, orgID)
		pool.Exec(context.Background(), `DELETE FROM notification.preferences WHERE auth_sub = ANY($1)`, members)
	})

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO notification.preferences (auth_sub, channel, event_type, enabled) VALUES ($1, 'in_app', 'organization_suspended', false)`,
		optedOut,
	); err != nil {
		t.Fatalf("insert in_app preference: %v", err)
	}

	mod := notification.New(pool, nil, notifMembersReader{members: members, name: "Acme"}, nil, "", nil)
	body := encodeEnvNotif("sus-pref-01", events.RoutingKeyOrganizationSuspended, orgID,
		events.OrganizationSuspended{OrganizationID: orgID, Reason: "test", SuspendedAt: time.Now()})

	if err := mod.Worker.HandleOrganizationSuspended(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationSuspended: %v", err)
	}

	rows, err := pool.Query(t.Context(),
		`SELECT auth_sub FROM notification.messages WHERE organization_id = $1 AND channel = 'organization_suspended'`, orgID)
	if err != nil {
		t.Fatalf("query messages: %v", err)
	}
	defer rows.Close()
	var notified []string
	for rows.Next() {
		var sub *string
		if err := rows.Scan(&sub); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if sub != nil {
			notified = append(notified, *sub)
		}
	}

	if len(notified) != len(members)-1 {
		t.Fatalf("want %d notified members (one opted out), got %d: %v", len(members)-1, len(notified), notified)
	}
	for _, sub := range notified {
		if sub == optedOut {
			t.Errorf("opted-out member %s must not receive a notification, got one anyway", optedOut)
		}
	}
}

func TestIntegration_OrganizationWideHandlers_NilReader_NoOp(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f04"
	mod := notification.NewModuleForTest(pool) // nil orgReader

	handlers := map[string]struct {
		fn   func(context.Context, []byte) error
		body []byte
	}{
		"suspended":   {mod.Worker.HandleOrganizationSuspended, encodeEnvNotif("s", events.RoutingKeyOrganizationSuspended, orgID, events.OrganizationSuspended{OrganizationID: orgID})},
		"reactivated": {mod.Worker.HandleOrganizationReactivated, encodeEnvNotif("r", events.RoutingKeyOrganizationReactivated, orgID, events.OrganizationReactivated{OrganizationID: orgID})},
		"deleted":     {mod.Worker.HandleOrganizationDeleted, encodeEnvNotif("d", events.RoutingKeyOrganizationDeleted, orgID, events.OrganizationDeleted{OrganizationID: orgID})},
	}
	for name, c := range handlers {
		if err := c.fn(t.Context(), c.body); err != nil {
			t.Errorf("%s with nil orgReader: want nil, got %v", name, err)
		}
	}

	var count int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1`, orgID).Scan(&count)
	if count != 0 {
		t.Errorf("nil orgReader must insert nothing, got %d rows", count)
	}
}

// --- owner-directed billing handlers ---

func TestIntegration_HandleTrialStarted_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f05"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("trial-01", events.RoutingKeyTrialStarted, orgID,
		events.TrialStarted{OrgID: orgID, Plan: "growth", TrialEnd: time.Now().Add(14 * 24 * time.Hour)})

	if err := mod.Worker.HandleTrialStarted(t.Context(), body); err != nil {
		t.Fatalf("HandleTrialStarted: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "trial_started")
}

func TestIntegration_HandleInvoiceFailed_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f06"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("fail-01", events.RoutingKeyInvoiceFailed, orgID,
		events.InvoiceFailed{OrgID: orgID, InvoiceID: "inv-f06", FailedAt: time.Now()})

	if err := mod.Worker.HandleInvoiceFailed(t.Context(), body); err != nil {
		t.Fatalf("HandleInvoiceFailed: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "invoice_failed")
}

func TestIntegration_HandleSubscriptionActivated_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f07"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("act-01", events.RoutingKeySubscriptionActivated, orgID,
		events.SubscriptionActivated{OrgID: orgID, Plan: "growth", ActivatedAt: time.Now()})

	if err := mod.Worker.HandleSubscriptionActivated(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionActivated: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "subscription_activated")
}

func TestIntegration_HandleSubscriptionCancelled_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f08"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("can-01", events.RoutingKeySubscriptionCancelled, orgID,
		events.SubscriptionCancelled{OrgID: orgID, CancelledAt: time.Now()})

	if err := mod.Worker.HandleSubscriptionCancelled(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionCancelled: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "subscription_cancelled")
}

// TestIntegration_HandleSubscriptionExpired_NotifiesOwner regression-tests
// that a true subscription expiry (billing.expireIfDue) notifies through its
// own "subscription_expired" channel, distinct from
// HandleSubscriptionCancelled's "subscription_cancelled" — the two used to
// share one event/routing key, which made expiry send the cancel-specific
// "access continues until the end of the current period" copy even though
// the organization is already suspended by the time this fires.
func TestIntegration_HandleSubscriptionExpired_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f09"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("exp-01", events.RoutingKeySubscriptionExpired, orgID,
		events.SubscriptionExpired{OrgID: orgID, ExpiredAt: time.Now()})

	if err := mod.Worker.HandleSubscriptionExpired(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionExpired: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "subscription_expired")
}

func TestIntegration_HandleSubscriptionResumed_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f10"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("res-01", events.RoutingKeySubscriptionResumed, orgID,
		events.SubscriptionResumed{OrgID: orgID, Plan: "growth", ResumedAt: time.Now()})

	if err := mod.Worker.HandleSubscriptionResumed(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionResumed: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "subscription_resumed")
}

func TestIntegration_HandleSubscriptionPaymentRemind_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f11"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("dun3-01", events.RoutingKeySubscriptionPaymentRemind, orgID,
		events.InvoiceFailed{OrgID: orgID, InvoiceID: "inv-f11", FailedAt: time.Now()})

	if err := mod.Worker.HandleSubscriptionPaymentRemind(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionPaymentRemind: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "invoice_payment_remind")
}

func TestIntegration_HandleSubscriptionPaymentFinal_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f12"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("dun7-01", events.RoutingKeySubscriptionPaymentFinal, orgID,
		events.InvoiceFailed{OrgID: orgID, InvoiceID: "inv-f12", FailedAt: time.Now()})

	if err := mod.Worker.HandleSubscriptionPaymentFinal(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionPaymentFinal: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "invoice_payment_final")
}

func TestIntegration_HandleMemberRoleChanged_NotifiesMember(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f13"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	body := encodeEnvNotif("role-01", events.RoutingKeyMemberRoleChanged, orgID,
		events.MemberRoleChanged{OrganizationID: orgID, AuthSub: "integ_sub_notif_role", Role: "admin"})

	if err := mod.Worker.HandleMemberRoleChanged(t.Context(), body); err != nil {
		t.Fatalf("HandleMemberRoleChanged: %v", err)
	}
	var count int
	var sampleBody string
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*), COALESCE(MAX(body),'') FROM notification.messages WHERE organization_id = $1 AND channel = 'member_role_changed'`, orgID,
	).Scan(&count, &sampleBody)
	if count == 0 {
		t.Fatal("expected member_role_changed notification")
	}
	if !strings.Contains(sampleBody, "admin") {
		t.Errorf("role-changed body should mention the new role, got %q", sampleBody)
	}
}

func TestIntegration_HandleOwnershipTransferred_NotifiesNewOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f14"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	body := encodeEnvNotif("own-01", events.RoutingKeyOwnershipTransferred, orgID,
		events.OwnershipTransferred{OrganizationID: orgID, PreviousOwner: "old_owner", NewOwner: "integ_sub_notif_newowner"})

	if err := mod.Worker.HandleOwnershipTransferred(t.Context(), body); err != nil {
		t.Fatalf("HandleOwnershipTransferred: %v", err)
	}
	// notification must target the NEW owner, never the previous one
	var forNew, forOld int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'ownership_transferred' AND auth_sub = 'integ_sub_notif_newowner'`, orgID,
	).Scan(&forNew)
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'ownership_transferred' AND auth_sub = 'old_owner'`, orgID,
	).Scan(&forOld)
	if forNew == 0 {
		t.Error("expected ownership_transferred notification for the new owner")
	}
	if forOld != 0 {
		t.Error("previous owner must NOT receive an ownership_transferred notification")
	}
}

func TestIntegration_HandleUsageLimitWarning_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f15"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("usage-01", events.RoutingKeyUsageLimitWarning, orgID,
		events.UsageLimitWarning{OrgID: orgID, Metric: "members", Current: 9, Limit: 10})

	if err := mod.Worker.HandleUsageLimitWarning(t.Context(), body); err != nil {
		t.Fatalf("HandleUsageLimitWarning: %v", err)
	}
	var count int
	var sampleBody string
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*), COALESCE(MAX(body),'') FROM notification.messages WHERE organization_id = $1 AND channel = 'usage_limit_warning'`, orgID,
	).Scan(&count, &sampleBody)
	if count == 0 {
		t.Fatal("expected usage_limit_warning notification")
	}
	if !strings.Contains(sampleBody, "9 / 10") {
		t.Errorf("usage warning body should carry the current/limit detail, got %q", sampleBody)
	}
}

func TestIntegration_HandleWebhookHealthWarning_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f16"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	// resolveOwnerEmail needs BOTH readers non-nil to yield a non-empty owner sub
	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	body := encodeEnvNotif("wh-health-01", events.RoutingKeyWebhookHealthWarning, orgID,
		events.WebhookHealthWarning{OrganizationID: orgID, URL: "https://hook.example.com", SuccessPercent: 42})

	if err := mod.Worker.HandleWebhookHealthWarning(t.Context(), body); err != nil {
		t.Fatalf("HandleWebhookHealthWarning: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "webhook_health_warning")
}

func TestIntegration_HandleWebhookAutoDisabled_NotifiesOwner(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f17"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	body := encodeEnvNotif("wh-off-01", events.RoutingKeyWebhookAutoDisabled, orgID,
		events.WebhookAutoDisabled{OrganizationID: orgID, URL: "https://hook.example.com"})

	if err := mod.Worker.HandleWebhookAutoDisabled(t.Context(), body); err != nil {
		t.Fatalf("HandleWebhookAutoDisabled: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "webhook_auto_disabled")
}

func TestIntegration_WebhookHandlers_UnresolvableOwner_NoOp(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f18"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	// nil readers → resolveOwnerEmail returns empty sub → handlers must no-op
	mod := notification.NewModuleForTest(pool)
	health := encodeEnvNotif("h", events.RoutingKeyWebhookHealthWarning, orgID, events.WebhookHealthWarning{OrganizationID: orgID, URL: "u"})
	off := encodeEnvNotif("o", events.RoutingKeyWebhookAutoDisabled, orgID, events.WebhookAutoDisabled{OrganizationID: orgID, URL: "u"})
	if err := mod.Worker.HandleWebhookHealthWarning(t.Context(), health); err != nil {
		t.Errorf("health warning, unresolvable owner: want nil, got %v", err)
	}
	if err := mod.Worker.HandleWebhookAutoDisabled(t.Context(), off); err != nil {
		t.Errorf("auto-disabled, unresolvable owner: want nil, got %v", err)
	}
	var count int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1`, orgID).Scan(&count)
	if count != 0 {
		t.Errorf("unresolvable owner must insert nothing, got %d", count)
	}
}

func TestIntegration_HandleInvitationRequested(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f19"

	body := encodeEnvNotif("invreq-01", events.RoutingKeyInvitationRequested, orgID,
		events.InvitationRequested{OrganizationID: orgID, InvitedBy: "integ_sub_inviter", InviteeEmail: "who@test.com"})

	// with a userReader resolving the inviter's email, the handler proceeds to
	// sendEmail (nil mailer → no-op) and returns nil
	modResolvable := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	if err := modResolvable.Worker.HandleInvitationRequested(t.Context(), body); err != nil {
		t.Errorf("HandleInvitationRequested (resolvable inviter): want nil, got %v", err)
	}

	// with no userReader the inviter email can't be resolved → early no-op return
	modNil := notification.NewModuleForTest(pool)
	if err := modNil.Worker.HandleInvitationRequested(t.Context(), body); err != nil {
		t.Errorf("HandleInvitationRequested (unresolvable inviter): want nil, got %v", err)
	}
}

func TestIntegration_HandleInvitationDeclined_NotifiesInviterInApp(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f20"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	body := encodeEnvNotif("invdecl-01", events.RoutingKeyInvitationDeclined, orgID,
		events.InvitationDeclined{OrganizationID: orgID, InvitedBy: "integ_sub_inviter", InviteeEmail: "who@test.com"})

	if err := mod.Worker.HandleInvitationDeclined(t.Context(), body); err != nil {
		t.Fatalf("HandleInvitationDeclined: %v", err)
	}
	assertNotifChannelExists(t, pool, orgID, "invitation_declined")
}

// --- email path (configured mailer) ---

func TestIntegration_SendEmail_ConfiguredMailer_RecordsFailedRow(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f20"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, deadMailer(), notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "https://app.test", nil)
	body := encodeEnvNotif("trial-mail-01", events.RoutingKeyTrialStarted, orgID,
		events.TrialStarted{OrgID: orgID, Plan: "growth", TrialEnd: time.Now().Add(14 * 24 * time.Hour)})

	if err := mod.Worker.HandleTrialStarted(t.Context(), body); err != nil {
		t.Fatalf("HandleTrialStarted with mailer: %v", err)
	}

	var count int
	var status string
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*), COALESCE(MAX(payload->>'status'),'') FROM notification.messages WHERE organization_id = $1 AND kind = 'email' AND channel = 'trial_started'`, orgID,
	).Scan(&count, &status)
	if count == 0 {
		t.Fatal("configured mailer should record an email message row")
	}
	if status != "failed" {
		t.Errorf("dead SMTP host should record status=failed, got %q", status)
	}
}

func TestIntegration_SendEmail_SkippedWhenEmailPreferenceDisabled(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f21"
	t.Cleanup(func() {
		cleanupNotifByOrganization(pool, orgID)
		pool.Exec(context.Background(), `DELETE FROM notification.preferences WHERE auth_sub = $1`, testAuthSubNotif)
	})

	// owner has opted out of the trial_started email specifically
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO notification.preferences (auth_sub, channel, event_type, enabled) VALUES ($1, 'email', 'trial_started', false)`,
		testAuthSubNotif,
	); err != nil {
		t.Fatalf("insert email preference: %v", err)
	}

	mod := notification.New(pool, deadMailer(), notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "https://app.test", nil)
	body := encodeEnvNotif("trial-mail-02", events.RoutingKeyTrialStarted, orgID,
		events.TrialStarted{OrgID: orgID, Plan: "growth", TrialEnd: time.Now().Add(14 * 24 * time.Hour)})
	if err := mod.Worker.HandleTrialStarted(t.Context(), body); err != nil {
		t.Fatalf("HandleTrialStarted: %v", err)
	}

	var emailCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND kind = 'email' AND channel = 'trial_started'`, orgID,
	).Scan(&emailCount)
	if emailCount != 0 {
		t.Errorf("email disabled by preference must not send/record, got %d email rows", emailCount)
	}
	// the in_app notification is governed by a separate preference and still fires
	assertNotifChannelExists(t, pool, orgID, "trial_started")
}

// --- decode-error edge cases: a malformed body must surface the decode error ---

func TestWorker_Handlers_DecodeError(t *testing.T) {
	pool := testPoolNotif(t)
	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	bad := []byte(`{not valid json`)

	handlers := map[string]func(context.Context, []byte) error{
		"suspended":   mod.Worker.HandleOrganizationSuspended,
		"reactivated": mod.Worker.HandleOrganizationReactivated,
		"deleted":     mod.Worker.HandleOrganizationDeleted,
		"trial":       mod.Worker.HandleTrialStarted,
		"failed":      mod.Worker.HandleInvoiceFailed,
		"activated":   mod.Worker.HandleSubscriptionActivated,
		"cancelled":   mod.Worker.HandleSubscriptionCancelled,
		"expired":     mod.Worker.HandleSubscriptionExpired,
	}
	for name, h := range handlers {
		if err := h(t.Context(), bad); err == nil {
			t.Errorf("%s: expected decode error on malformed body, got nil", name)
		}
	}
}

// --- preferences CRUD through the HTTP surface ---

func TestIntegration_Preferences_UpsertThenList(t *testing.T) {
	pool := testPoolNotif(t)
	const authSub = "integ_sub_notif_prefs_crud"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM notification.preferences WHERE auth_sub = $1`, authSub)
	})

	e := notification.NewModuleEngine(pool, authSub)

	// upsert: disable the invoice_failed email
	wUp := httptest.NewRecorder()
	e.ServeHTTP(wUp, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/preferences",
		`{"channel":"email","event_type":"invoice_failed","enabled":false}`))
	if wUp.Code != http.StatusOK {
		t.Fatalf("upsert preference: want 200, got %d: %s", wUp.Code, wUp.Body)
	}

	// upsert again (same key) flips it back on — exercises the ON CONFLICT update path
	wUp2 := httptest.NewRecorder()
	e.ServeHTTP(wUp2, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/preferences",
		`{"channel":"email","event_type":"invoice_failed","enabled":true}`))
	if wUp2.Code != http.StatusOK {
		t.Fatalf("re-upsert preference: want 200, got %d: %s", wUp2.Code, wUp2.Body)
	}

	// list: exactly one row, reflecting the latest enabled=true
	wList := httptest.NewRecorder()
	e.ServeHTTP(wList, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications/preferences", ""))
	if wList.Code != http.StatusOK {
		t.Fatalf("list preferences: want 200, got %d: %s", wList.Code, wList.Body)
	}
	var resp struct {
		Data []struct {
			Channel   string `json:"channel"`
			EventType string `json:"event_type"`
			Enabled   bool   `json:"enabled"`
		} `json:"data"`
	}
	json.NewDecoder(wList.Body).Decode(&resp)
	if len(resp.Data) != 1 {
		t.Fatalf("want exactly 1 preference row after two upserts of the same key, got %d", len(resp.Data))
	}
	got := resp.Data[0]
	if got.Channel != "email" || got.EventType != "invoice_failed" || !got.Enabled {
		t.Errorf("preference not updated correctly: %+v", got)
	}
}

func TestIntegration_Preferences_Upsert_MissingChannel_422(t *testing.T) {
	pool := testPoolNotif(t)
	e := notification.NewModuleEngine(pool, "integ_sub_notif_prefs_badreq")

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/preferences",
		`{"event_type":"invoice_failed","enabled":true}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing required channel: want 422, got %d: %s", w.Code, w.Body)
	}
}

// --- GDPR contract methods: list-for-export and delete-all ---

func TestIntegration_GDPR_ListForUserThenDeleteAll(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f09"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	// seed an in_app message for testAuthSubNotif via a worker handler
	seed := notification.NewModuleForTest(pool)
	if err := seed.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedForNotif(orgID)); err != nil {
		t.Fatalf("seed org-created: %v", err)
	}

	mod := notification.NewModuleForTest(pool)

	exported, err := mod.ListForUser(t.Context(), testAuthSubNotif, 100)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(exported) == 0 {
		t.Fatal("ListForUser: want at least one exported message, got 0")
	}
	for _, m := range exported {
		if m.ID == "" || m.Channel == "" {
			t.Errorf("exported message missing fields: %+v", m)
		}
	}

	if err := mod.DeleteAllForUser(t.Context(), testAuthSubNotif); err != nil {
		t.Fatalf("DeleteAllForUser: %v", err)
	}

	after, err := mod.ListForUser(t.Context(), testAuthSubNotif, 100)
	if err != nil {
		t.Fatalf("ListForUser after delete: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("after DeleteAllForUser: want 0 messages, got %d", len(after))
	}
}
