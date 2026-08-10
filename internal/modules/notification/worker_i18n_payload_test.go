package notification_test

import (
	"encoding/json"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/notification"
)

// assertPayloadKeys queries the in-app message payload for a channel and
// asserts its key set matches exactly what the payload contract table
// promises the frontend i18n catalog can interpolate on. A drifted key here
// (added, renamed, or dropped in worker.go without updating the contract)
// would render a literal "{{var}}" in the UI instead of a translated string.
func assertPayloadKeys(t *testing.T, pool *pgxpool.Pool, orgID, channel string, want ...string) {
	t.Helper()
	var raw []byte
	err := pool.QueryRow(t.Context(),
		`SELECT payload FROM notification.messages WHERE organization_id = $1 AND channel = $2 AND kind = 'in_app' LIMIT 1`,
		orgID, channel,
	).Scan(&raw)
	if err != nil {
		t.Fatalf("query payload for channel %q: %v", channel, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode payload for channel %q: %v", channel, err)
	}
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if !slices.Equal(got, wantSorted) {
		t.Errorf("channel %q payload keys = %v, want %v", channel, got, wantSorted)
	}
}

func TestIntegration_PayloadKeys_Welcome(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000001"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedForNotif(orgID)); err != nil {
		t.Fatalf("HandleOrganizationCreated: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "welcome", "organization_name")
}

func TestIntegration_PayloadKeys_OrganizationSuspended(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000002"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{members: []string{"integ_sub_i18n_1"}, name: "Acme"}, nil, "", nil)
	body := encodeEnvNotif("i18n-sus-01", events.RoutingKeyOrganizationSuspended, orgID,
		events.OrganizationSuspended{OrganizationID: orgID, Reason: "unpaid invoice", SuspendedAt: time.Now()})
	if err := mod.Worker.HandleOrganizationSuspended(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationSuspended: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "organization_suspended", "reason")
}

func TestIntegration_PayloadKeys_OrganizationReactivated(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000003"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{members: []string{"integ_sub_i18n_2"}, name: "Acme"}, nil, "", nil)
	body := encodeEnvNotif("i18n-react-01", events.RoutingKeyOrganizationReactivated, orgID,
		events.OrganizationReactivated{OrganizationID: orgID, ReactivatedAt: time.Now()})
	if err := mod.Worker.HandleOrganizationReactivated(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationReactivated: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "organization_reactivated")
}

func TestIntegration_PayloadKeys_OrganizationDeleted(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000004"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{members: []string{"integ_sub_i18n_3"}, name: "Globex"}, nil, "", nil)
	body := encodeEnvNotif("i18n-del-01", events.RoutingKeyOrganizationDeleted, orgID,
		events.OrganizationDeleted{OrganizationID: orgID, DeletedAt: time.Now()})
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationDeleted: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "organization_deleted", "organization_name")
}

func TestIntegration_PayloadKeys_InvoicePaid(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000005"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-paid-01", events.RoutingKeyInvoicePaid, orgID,
		events.InvoicePaid{OrgID: orgID, InvoiceID: "inv-i18n-01", PaidAt: time.Now()})
	if err := mod.Worker.HandleInvoicePaid(t.Context(), body); err != nil {
		t.Fatalf("HandleInvoicePaid: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invoice_paid", "invoice_id")
}

func TestIntegration_PayloadKeys_SubscriptionRemind(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000006"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleSubscriptionRemind(t.Context(), encodeSubscriptionRemindForNotif("sub-i18n-06", orgID)); err != nil {
		t.Fatalf("HandleSubscriptionRemind: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "subscription_remind", "expected_end", "is_trial")
}

func TestIntegration_PayloadKeys_TrialStarted(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000007"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-trial-01", events.RoutingKeyTrialStarted, orgID,
		events.TrialStarted{OrgID: orgID, Plan: "growth", TrialEnd: time.Now().Add(14 * 24 * time.Hour)})
	if err := mod.Worker.HandleTrialStarted(t.Context(), body); err != nil {
		t.Fatalf("HandleTrialStarted: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "trial_started", "trial_end")
}

func TestIntegration_PayloadKeys_InvoiceCreated(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000008"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleInvoiceCreated(t.Context(), encodeInvoiceCreatedForNotif(orgID, "inv-i18n-08")); err != nil {
		t.Fatalf("HandleInvoiceCreated: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invoice_created", "invoice_id", "from_trial")
}

func TestIntegration_PayloadKeys_InvoiceFailed(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000009"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-fail-01", events.RoutingKeyInvoiceFailed, orgID,
		events.InvoiceFailed{OrgID: orgID, InvoiceID: "inv-i18n-09", FailedAt: time.Now()})
	if err := mod.Worker.HandleInvoiceFailed(t.Context(), body); err != nil {
		t.Fatalf("HandleInvoiceFailed: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invoice_failed", "invoice_id")
}

func TestIntegration_PayloadKeys_SubscriptionActivated(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000010"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-act-01", events.RoutingKeySubscriptionActivated, orgID,
		events.SubscriptionActivated{OrgID: orgID, Plan: "growth", ActivatedAt: time.Now()})
	if err := mod.Worker.HandleSubscriptionActivated(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionActivated: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "subscription_activated", "plan")
}

func TestIntegration_PayloadKeys_SubscriptionCancelled(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000011"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-can-01", events.RoutingKeySubscriptionCancelled, orgID,
		events.SubscriptionCancelled{OrgID: orgID, CancelledAt: time.Now()})
	if err := mod.Worker.HandleSubscriptionCancelled(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionCancelled: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "subscription_cancelled")
}

func TestIntegration_PayloadKeys_SubscriptionExpired(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000012"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-exp-01", events.RoutingKeySubscriptionExpired, orgID,
		events.SubscriptionExpired{OrgID: orgID, ExpiredAt: time.Now()})
	if err := mod.Worker.HandleSubscriptionExpired(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionExpired: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "subscription_expired")
}

func TestIntegration_PayloadKeys_SubscriptionResumed(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000013"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-res-01", events.RoutingKeySubscriptionResumed, orgID,
		events.SubscriptionResumed{OrgID: orgID, Plan: "growth", ResumedAt: time.Now()})
	if err := mod.Worker.HandleSubscriptionResumed(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionResumed: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "subscription_resumed", "plan")
}

func TestIntegration_PayloadKeys_InvoicePaymentRemind(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000014"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-dun3-01", events.RoutingKeySubscriptionPaymentRemind, orgID,
		events.InvoiceFailed{OrgID: orgID, InvoiceID: "inv-i18n-14", FailedAt: time.Now()})
	if err := mod.Worker.HandleSubscriptionPaymentRemind(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionPaymentRemind: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invoice_payment_remind", "invoice_id")
}

func TestIntegration_PayloadKeys_InvoicePaymentFinal(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000015"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-dun7-01", events.RoutingKeySubscriptionPaymentFinal, orgID,
		events.InvoiceFailed{OrgID: orgID, InvoiceID: "inv-i18n-15", FailedAt: time.Now()})
	if err := mod.Worker.HandleSubscriptionPaymentFinal(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionPaymentFinal: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invoice_payment_final", "invoice_id")
}

func TestIntegration_PayloadKeys_Invite(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000016"
	const authSub = "integ_sub_i18n_invite"
	const email = "i18n-invitee@test.com"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, nil, stubMemberInvitedUserReader{authSub: authSub, email: email}, "", nil)
	body := encodeEnvNotif("i18n-invite-01", events.RoutingKeyMemberInvited, orgID,
		events.MemberInvited{OrganizationID: orgID, Email: email, Role: "member", InvitedBy: testAuthSubNotif})
	if err := mod.Worker.HandleMemberInvited(t.Context(), body); err != nil {
		t.Fatalf("HandleMemberInvited: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invite", "organization_name")
}

func TestIntegration_PayloadKeys_InvitationRequested(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000017"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	body := encodeEnvNotif("i18n-invreq-01", events.RoutingKeyInvitationRequested, orgID,
		events.InvitationRequested{OrganizationID: orgID, InvitedBy: "integ_sub_i18n_inviter", InviteeEmail: "who@test.com"})
	if err := mod.Worker.HandleInvitationRequested(t.Context(), body); err != nil {
		t.Fatalf("HandleInvitationRequested: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invitation_requested", "invitee_email", "organization_name")
}

func TestIntegration_PayloadKeys_InvitationDeclined(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000018"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	body := encodeEnvNotif("i18n-invdecl-01", events.RoutingKeyInvitationDeclined, orgID,
		events.InvitationDeclined{OrganizationID: orgID, InvitedBy: "integ_sub_i18n_inviter", InviteeEmail: "who@test.com"})
	if err := mod.Worker.HandleInvitationDeclined(t.Context(), body); err != nil {
		t.Fatalf("HandleInvitationDeclined: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "invitation_declined", "invitee_email", "organization_name")
}

func TestIntegration_PayloadKeys_MemberRemoved(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000019"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, nil, "", nil)
	body := encodeEnvNotif("i18n-remove-01", events.RoutingKeyMemberRemoved, orgID,
		events.MemberRemoved{OrganizationID: orgID, AuthSub: "integ_sub_i18n_removed"})
	if err := mod.Worker.HandleMemberRemoved(t.Context(), body); err != nil {
		t.Fatalf("HandleMemberRemoved: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "member_removed", "organization_name")
}

func TestIntegration_PayloadKeys_MemberRoleChanged(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000020"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	body := encodeEnvNotif("i18n-role-01", events.RoutingKeyMemberRoleChanged, orgID,
		events.MemberRoleChanged{OrganizationID: orgID, AuthSub: "integ_sub_i18n_role", Role: "admin"})
	if err := mod.Worker.HandleMemberRoleChanged(t.Context(), body); err != nil {
		t.Fatalf("HandleMemberRoleChanged: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "member_role_changed", "organization_name", "role")
}

func TestIntegration_PayloadKeys_OwnershipTransferred(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000021"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	body := encodeEnvNotif("i18n-own-01", events.RoutingKeyOwnershipTransferred, orgID,
		events.OwnershipTransferred{OrganizationID: orgID, PreviousOwner: "old_owner", NewOwner: "integ_sub_i18n_newowner"})
	if err := mod.Worker.HandleOwnershipTransferred(t.Context(), body); err != nil {
		t.Fatalf("HandleOwnershipTransferred: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "ownership_transferred", "organization_name")
}

func TestIntegration_PayloadKeys_WebhookHealthWarning(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000022"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	body := encodeEnvNotif("i18n-wh-health-01", events.RoutingKeyWebhookHealthWarning, orgID,
		events.WebhookHealthWarning{OrganizationID: orgID, URL: "https://hook.example.com", SuccessPercent: 42})
	if err := mod.Worker.HandleWebhookHealthWarning(t.Context(), body); err != nil {
		t.Fatalf("HandleWebhookHealthWarning: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "webhook_health_warning", "url", "success_percent")
}

func TestIntegration_PayloadKeys_WebhookAutoDisabled(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000023"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, notifMembersReader{name: "Acme"}, stubNotifUserReader{}, "", nil)
	body := encodeEnvNotif("i18n-wh-off-01", events.RoutingKeyWebhookAutoDisabled, orgID,
		events.WebhookAutoDisabled{OrganizationID: orgID, URL: "https://hook.example.com"})
	if err := mod.Worker.HandleWebhookAutoDisabled(t.Context(), body); err != nil {
		t.Fatalf("HandleWebhookAutoDisabled: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "webhook_auto_disabled", "url")
}

func TestIntegration_PayloadKeys_EmailChanged(t *testing.T) {
	pool := testPoolNotif(t)
	// stubNotifWsReader.GetFirstOrganizationIDForMember always anchors to this
	// fixed organization, same as repository_test.go's own use of it.
	const orgID = "00000000-0000-0000-0000-000000000e08"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, stubNotifWsReader{}, nil, "", nil)
	if err := mod.Worker.HandleUserEmailChanged(t.Context(), encodeUserEmailChanged(testAuthSubNotif, "old@example.com", "new@example.com")); err != nil {
		t.Fatalf("HandleUserEmailChanged: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "email_changed", "old_email", "new_email")
}

func TestIntegration_PayloadKeys_UsageLimitWarning(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0001-000000000024"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif("i18n-usage-01", events.RoutingKeyUsageLimitWarning, orgID,
		events.UsageLimitWarning{OrgID: orgID, Metric: "members", Current: 9, Limit: 10})
	if err := mod.Worker.HandleUsageLimitWarning(t.Context(), body); err != nil {
		t.Fatalf("HandleUsageLimitWarning: %v", err)
	}
	assertPayloadKeys(t, pool, orgID, "usage_limit_warning", "metric", "current", "limit")
}
