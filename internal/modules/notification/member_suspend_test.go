package notification_test

import (
	"testing"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/notification"
)

// assertInAppOnly checks that a member-suspend/reinstate handler wrote
// exactly one in-app message to the affected member and attempted no email —
// the same shape TestIntegration_HandleMemberRoleChanged_NotifiesMember asserts.
func assertInAppOnly(t *testing.T, orgID, channel, authSub string) {
	t.Helper()
	pool := testPoolNotif(t)

	var inApp int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages
		 WHERE organization_id = $1 AND channel = $2 AND kind = 'in_app' AND auth_sub = $3`,
		orgID, channel, authSub,
	).Scan(&inApp)
	if inApp != 1 {
		t.Errorf("in-app messages for %s: want 1, got %d", channel, inApp)
	}

	var email int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND kind = 'email'`,
		orgID,
	).Scan(&email)
	if email != 0 {
		t.Errorf("email messages for %s: want 0 (in-app only), got %d", channel, email)
	}
}

func TestIntegration_HandleMemberSuspended_NotifiesMemberInAppOnly(t *testing.T) {
	const orgID = "00000000-0000-0000-0000-000000000f30"
	const authSub = "integ_sub_notif_suspended"
	pool := testPoolNotif(t)
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	body := encodeEnvNotif("mem-susp-01", events.RoutingKeyMemberSuspended, orgID,
		events.MemberSuspended{OrganizationID: orgID, AuthSub: authSub})

	if err := mod.Worker.HandleMemberSuspended(t.Context(), body); err != nil {
		t.Fatalf("HandleMemberSuspended: %v", err)
	}
	assertInAppOnly(t, orgID, "member_suspended", authSub)
}

func TestIntegration_HandleMemberReinstated_NotifiesMemberInAppOnly(t *testing.T) {
	const orgID = "00000000-0000-0000-0000-000000000f31"
	const authSub = "integ_sub_notif_reinstated"
	pool := testPoolNotif(t)
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	body := encodeEnvNotif("mem-reinst-01", events.RoutingKeyMemberReinstated, orgID,
		events.MemberReinstated{OrganizationID: orgID, AuthSub: authSub})

	if err := mod.Worker.HandleMemberReinstated(t.Context(), body); err != nil {
		t.Fatalf("HandleMemberReinstated: %v", err)
	}
	assertInAppOnly(t, orgID, "member_reinstated", authSub)
}
