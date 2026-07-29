package notification_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

const testAuthSubNotif = "integ_sub_notif_1"

func testPoolNotif(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cleanupNotifByOrganization(pool *pgxpool.Pool, organizationID string) {
	pool.Exec(context.Background(),
		`DELETE FROM notification.messages WHERE organization_id = $1`, organizationID)
}

func encodeOrganizationCreatedForNotif(organizationID string) []byte {
	env := events.Envelope{
		ID: "notif-evt-" + organizationID, Type: events.RoutingKeyOrganizationCreated,
		Source: "organization", Time: time.Now(), OrgID: organizationID,
		Data: events.OrganizationCreated{
			OrganizationID: organizationID, Slug: "notif-ws",
			Name: "Notif WS", CreatedBy: testAuthSubNotif, CreatedAt: time.Now(),
		},
	}
	b, _ := json.Marshal(env)
	return b
}

func encodeInvoiceCreatedForNotif(orgID, invoiceID string) []byte {
	env := events.Envelope{
		ID: "notif-inv-" + invoiceID, Type: events.RoutingKeyInvoiceCreated,
		Source: "billing", Time: time.Now(), OrgID: orgID,
		Data: events.InvoiceCreated{OrgID: orgID, InvoiceID: invoiceID},
	}
	b, _ := json.Marshal(env)
	return b
}

func encodeSubscriptionRemindForNotif(subID, subjectID string) []byte {
	env := events.Envelope{
		ID: "notif-remind-" + subID, Type: events.RoutingKeySubscriptionRemind,
		Source: "billing", Time: time.Now(), OrgID: subjectID,
		Data: events.SubscriptionCheck{
			SubscriptionID: subID, SubjectType: "organization",
			SubjectID: subjectID, ExpectedEnd: time.Now().Add(3 * 24 * time.Hour),
		},
	}
	b, _ := json.Marshal(env)
	return b
}

func TestIntegration_HandleInvoiceCreated_InsertsNotification(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e04"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleInvoiceCreated(t.Context(), encodeInvoiceCreatedForNotif(orgID, "inv-e04-01")); err != nil {
		t.Fatalf("HandleInvoiceCreated: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'invoice_created'`, orgID,
	).Scan(&count)
	if count == 0 {
		t.Error("expected invoice_created notification to be inserted")
	}
}

func TestIntegration_HandleSubscriptionRemind_InsertsNotification(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e05"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleSubscriptionRemind(t.Context(), encodeSubscriptionRemindForNotif("sub-e05", orgID)); err != nil {
		t.Fatalf("HandleSubscriptionRemind: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'subscription_remind'`, orgID,
	).Scan(&count)
	if count == 0 {
		t.Error("expected subscription_remind notification to be inserted")
	}
}

func encodeMemberRemovedForNotif(orgID, authSub string) []byte {
	env := events.Envelope{
		ID: "notif-member-removed-" + authSub, Type: events.RoutingKeyMemberRemoved,
		Source: "organization", Time: time.Now(), OrgID: orgID,
		Data: events.MemberRemoved{OrganizationID: orgID, AuthSub: authSub},
	}
	b, _ := json.Marshal(env)
	return b
}

// HandleMemberRemoved reads its target authSub straight from the event
// payload (not via orgReader), so it exercises service.send()'s preference
// check even with NewModuleForTest's nil orgReader/userReader.
func TestIntegration_Send_SkipsWhenInAppPreferenceDisabled(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e09"
	const authSub = "integ_sub_notif_pref_disabled"
	t.Cleanup(func() {
		cleanupNotifByOrganization(pool, orgID)
		pool.Exec(context.Background(), `DELETE FROM notification.preferences WHERE auth_sub = $1`, authSub)
	})

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO notification.preferences (auth_sub, channel, event_type, enabled) VALUES ($1, 'in_app', 'member_removed', false)`,
		authSub,
	); err != nil {
		t.Fatalf("insert preference: %v", err)
	}

	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleMemberRemoved(t.Context(), encodeMemberRemovedForNotif(orgID, authSub)); err != nil {
		t.Fatalf("HandleMemberRemoved: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'member_removed'`, orgID,
	).Scan(&count)
	if count != 0 {
		t.Errorf("expected no notification when in_app preference is disabled, got %d", count)
	}
}

func TestIntegration_Send_InsertsWhenNoPreferenceRow(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e0a"
	const authSub = "integ_sub_notif_pref_default"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleMemberRemoved(t.Context(), encodeMemberRemovedForNotif(orgID, authSub)); err != nil {
		t.Fatalf("HandleMemberRemoved: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'member_removed'`, orgID,
	).Scan(&count)
	if count == 0 {
		t.Error("expected notification to be inserted when no preference row exists (fail-open default)")
	}
}

func encodeUserEmailChanged(authSub, oldEmail, newEmail string) []byte {
	env := events.Envelope{
		ID: "notif-email-changed-" + authSub, Type: events.RoutingKeyUserEmailChanged,
		Source: "account", Time: time.Now(),
		Data: events.UserEmailChanged{
			AuthSub: authSub, OldEmail: oldEmail, NewEmail: newEmail, ChangedAt: time.Now(),
		},
	}
	b, _ := json.Marshal(env)
	return b
}

func TestIntegration_HandleUserEmailChanged_InsertsNotification(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e08"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	// stubNotifWsReader.GetFirstOrganizationIDForMember anchors this to orgID
	mod := notification.New(pool, nil, stubNotifWsReader{}, nil, "", nil)

	evtBody := encodeUserEmailChanged(testAuthSubNotif, "old@example.com", "new@example.com")
	if err := mod.Worker.HandleUserEmailChanged(t.Context(), evtBody); err != nil {
		t.Fatalf("HandleUserEmailChanged: %v", err)
	}

	var msgBody, channel string
	err := pool.QueryRow(t.Context(),
		`SELECT body, channel FROM notification.messages WHERE organization_id = $1 AND channel = 'email_changed'`, orgID,
	).Scan(&msgBody, &channel)
	if err != nil {
		t.Fatalf("expected email_changed notification row, query failed: %v", err)
	}
	if !strings.Contains(msgBody, "old@example.com") || !strings.Contains(msgBody, "new@example.com") {
		t.Errorf("notification body: want both old and new email mentioned, got %q", msgBody)
	}
}

func TestIntegration_HandleUserEmailChanged_NoOrganization_NoOp(t *testing.T) {
	pool := testPoolNotif(t)
	// markReadOwnerReader.GetFirstOrganizationIDForMember returns "" — simulates
	// a user who changed email before joining any organization.
	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)

	body := encodeUserEmailChanged("sub_no_org_yet", "old@example.com", "new@example.com")
	if err := mod.Worker.HandleUserEmailChanged(t.Context(), body); err != nil {
		t.Errorf("HandleUserEmailChanged with no organization: want nil, got %v", err)
	}
}

func TestIntegration_HandleMemberInvited_NilWsReader_NoOp(t *testing.T) {
	pool := testPoolNotif(t)
	mod := notification.NewModuleForTest(pool)

	env := events.Envelope{
		ID: "notif-invite-01", Type: events.RoutingKeyMemberInvited,
		Source: "organization", Time: time.Now(),
		Data: events.MemberInvited{
			OrganizationID: "00000000-0000-0000-0000-000000000e06",
			Email:          "invite@test.com",
			Role:           "member",
			InvitedBy:      testAuthSubNotif,
		},
	}
	b, _ := json.Marshal(env)
	if err := mod.Worker.HandleMemberInvited(t.Context(), b); err != nil {
		t.Errorf("HandleMemberInvited with nil orgReader: want nil, got %v", err)
	}
}

// stubMemberInvitedUserReader resolves exactly one known email to a fixed
// auth_sub, everything else "not found" — backs the two
// HandleMemberInvited in-app-notification tests below.
type stubMemberInvitedUserReader struct {
	authSub string
	email   string
}

func (s stubMemberInvitedUserReader) GetUserByAuthSub(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, nil
}

func (s stubMemberInvitedUserReader) GetUserByEmail(_ context.Context, email string) (*contracts.UserInfo, error) {
	if email == s.email {
		return &contracts.UserInfo{AuthSub: s.authSub, Email: email}, nil
	}
	return nil, nil
}

func (s stubMemberInvitedUserReader) GetUsersByAuthSubs(_ context.Context, _ []string) (map[string]contracts.UserInfo, error) {
	return nil, nil
}

func (s stubMemberInvitedUserReader) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestIntegration_HandleMemberInvited_ExistingAccount_SendsInAppNotification(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e0b"
	const authSub = "integ_sub_notif_invite_existing"
	const email = "existing-invitee@test.com"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, nil,
		stubMemberInvitedUserReader{authSub: authSub, email: email}, "", nil)

	env := events.Envelope{
		ID: "notif-invite-existing-01", Type: events.RoutingKeyMemberInvited,
		Source: "organization", Time: time.Now(),
		Data: events.MemberInvited{
			OrganizationID: orgID, Email: email, Role: "member", InvitedBy: testAuthSubNotif,
		},
	}
	b, _ := json.Marshal(env)
	if err := mod.Worker.HandleMemberInvited(t.Context(), b); err != nil {
		t.Fatalf("HandleMemberInvited: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages
		 WHERE organization_id = $1 AND channel = 'invite' AND kind = 'in_app' AND auth_sub = $2`,
		orgID, authSub,
	).Scan(&count)
	if count == 0 {
		t.Error("expected an in-app notification for an invitee who already has an account")
	}
}

func TestIntegration_HandleMemberInvited_UnknownEmail_NoInAppNotification(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e0c"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	mod := notification.New(pool, nil, nil,
		stubMemberInvitedUserReader{authSub: "unused", email: "someone-else@test.com"}, "", nil)

	env := events.Envelope{
		ID: "notif-invite-unknown-01", Type: events.RoutingKeyMemberInvited,
		Source: "organization", Time: time.Now(),
		Data: events.MemberInvited{
			OrganizationID: orgID, Email: "brand-new@test.com", Role: "member", InvitedBy: testAuthSubNotif,
		},
	}
	b, _ := json.Marshal(env)
	if err := mod.Worker.HandleMemberInvited(t.Context(), b); err != nil {
		t.Fatalf("HandleMemberInvited: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'invite'`,
		orgID,
	).Scan(&count)
	if count != 0 {
		t.Errorf("expected no in-app notification for an unregistered email, got %d", count)
	}
}

func TestIntegration_ListNotifications_OrganizationScoped(t *testing.T) {
	pool := testPoolNotif(t)
	const (
		wsA = "00000000-0000-0000-0000-000000000e01"
		wsB = "00000000-0000-0000-0000-000000000e02"
	)
	t.Cleanup(func() {
		cleanupNotifByOrganization(pool, wsA)
		cleanupNotifByOrganization(pool, wsB)
	})

	mod := notification.NewModuleForTest(pool)
	ctx := t.Context()

	// insert welcome notification for organization A and B — both for testAuthSubNotif
	if err := mod.Worker.HandleOrganizationCreated(ctx, encodeOrganizationCreatedForNotif(wsA)); err != nil {
		t.Fatalf("insert ws A notification: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(ctx, encodeOrganizationCreatedForNotif(wsB)); err != nil {
		t.Fatalf("insert ws B notification: %v", err)
	}

	// list filtered to organization A — should not include ws B's message
	e := notification.NewModuleEngine(pool, testAuthSubNotif)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications?organization_id="+wsA, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("list notifications: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	messages := resp["data"].([]any)

	for _, m := range messages {
		msg := m.(map[string]any)
		if msg["organization_id"] != wsA {
			t.Errorf("organization_id scoping violated: got message for organization %v", msg["organization_id"])
		}
	}
	if len(messages) == 0 {
		t.Error("expected at least one notification for organization A")
	}
}

func TestIntegration_MarkRead_UpdatesOnlyTargetMessage(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e03"
	cleanupNotifByOrganization(pool, orgID) // pre-clean: ensures a fresh slate on repeated runs
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	ctx := t.Context()

	// insert first notification via organization-created worker (no orgReader needed)
	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleOrganizationCreated(ctx, encodeOrganizationCreatedForNotif(orgID)); err != nil {
		t.Fatalf("insert first notification: %v", err)
	}

	// insert second notification via invoice-paid worker; needs an orgReader that resolves
	// testAuthSubNotif as owner so the notification is stored for the correct auth_sub.
	modWithReader := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	env := events.Envelope{
		ID: "notif-evt2-" + orgID, Type: events.RoutingKeyInvoicePaid,
		Source: "billing", Time: time.Now(), OrgID: orgID,
		Data: events.InvoicePaid{OrgID: orgID, InvoiceID: "inv-test-01", PaidAt: time.Now()},
	}
	b, _ := json.Marshal(env)
	if err := modWithReader.Worker.HandleInvoicePaid(ctx, b); err != nil {
		t.Fatalf("insert second notification: %v", err)
	}

	e := notification.NewModuleEngine(pool, testAuthSubNotif)

	// unread count should be 2
	wCount := httptest.NewRecorder()
	e.ServeHTTP(wCount, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications/unread-count", ""))
	if wCount.Code != http.StatusOK {
		t.Fatalf("unread-count: want 200, got %d", wCount.Code)
	}
	var countResp map[string]any
	json.NewDecoder(wCount.Body).Decode(&countResp)
	if countResp["data"].(map[string]any)["unread"].(float64) < 2 {
		t.Errorf("want unread >= 2, got %v", countResp["data"])
	}

	// get the first notification's ID
	wList := httptest.NewRecorder()
	e.ServeHTTP(wList, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications", ""))
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	firstID := listResp["data"].([]any)[0].(map[string]any)["id"].(string)

	// mark first message as read
	wMark := httptest.NewRecorder()
	e.ServeHTTP(wMark, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/"+firstID+"/read", ""))
	if wMark.Code != http.StatusNoContent {
		t.Errorf("mark read: want 204, got %d: %s", wMark.Code, wMark.Body)
	}

	// unread count should now be 1
	wCount2 := httptest.NewRecorder()
	e.ServeHTTP(wCount2, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications/unread-count", ""))
	var countResp2 map[string]any
	json.NewDecoder(wCount2.Body).Decode(&countResp2)
	if countResp2["data"].(map[string]any)["unread"].(float64) != 1 {
		t.Errorf("after marking one read: want unread=1, got %v", countResp2["data"])
	}

	// mark-all-read clears the remaining unread message too
	wMarkAll := httptest.NewRecorder()
	e.ServeHTTP(wMarkAll, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/read-all", ""))
	if wMarkAll.Code != http.StatusNoContent {
		t.Errorf("mark all read: want 204, got %d: %s", wMarkAll.Code, wMarkAll.Body)
	}

	wCount3 := httptest.NewRecorder()
	e.ServeHTTP(wCount3, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications/unread-count", ""))
	var countResp3 map[string]any
	json.NewDecoder(wCount3.Body).Decode(&countResp3)
	if countResp3["data"].(map[string]any)["unread"].(float64) != 0 {
		t.Errorf("after mark-all-read: want unread=0, got %v", countResp3["data"])
	}
}

// TestIntegration_MarkRead_ScopedToOwnAuthSub confirms markRead cannot be used
// to flip another user's notification: the id-only WHERE clause would
// otherwise let any authenticated caller mark any notification read by
// guessing its id.
func TestIntegration_MarkRead_ScopedToOwnAuthSub(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e03"
	cleanupNotifByOrganization(pool, orgID)
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	ctx := t.Context()
	mod := notification.NewModuleForTest(pool)
	if err := mod.Worker.HandleOrganizationCreated(ctx, encodeOrganizationCreatedForNotif(orgID)); err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	eOwner := notification.NewModuleEngine(pool, testAuthSubNotif)
	wList := httptest.NewRecorder()
	eOwner.ServeHTTP(wList, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications", ""))
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	targetID := listResp["data"].([]any)[0].(map[string]any)["id"].(string)

	// a different caller tries to mark it read — should silently no-op, not succeed
	eOther := notification.NewModuleEngine(pool, "sub_someone_else")
	wMark := httptest.NewRecorder()
	eOther.ServeHTTP(wMark, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/"+targetID+"/read", ""))
	if wMark.Code != http.StatusNoContent {
		t.Fatalf("mark read (other user): want 204, got %d: %s", wMark.Code, wMark.Body)
	}

	wCount := httptest.NewRecorder()
	eOwner.ServeHTTP(wCount, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications/unread-count", ""))
	var countResp map[string]any
	json.NewDecoder(wCount.Body).Decode(&countResp)
	if countResp["data"].(map[string]any)["unread"].(float64) != 1 {
		t.Errorf("other user's markRead must not affect owner's unread count: got %v", countResp["data"])
	}
}

// --- resolveOwnerEmail with non-nil readers ---

// markReadOwnerReader resolves the owner as testAuthSubNotif for the MarkRead test.
type markReadOwnerReader struct{}

func (markReadOwnerReader) GetOrganizationByID(_ context.Context, _ string) (*contracts.OrganizationInfo, error) {
	return &contracts.OrganizationInfo{OwnerID: testAuthSubNotif}, nil
}
func (markReadOwnerReader) IsMember(_ context.Context, _, _ string) (bool, error) { return true, nil }
func (markReadOwnerReader) GetMemberRole(_ context.Context, _, _ string) (string, error) {
	return "owner", nil
}
func (markReadOwnerReader) ListMemberAuthSubs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (markReadOwnerReader) GetFirstOrganizationIDForMember(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (markReadOwnerReader) ListMembershipsForExport(_ context.Context, _ string) ([]contracts.OrgMembershipInfo, error) {
	return nil, nil
}
func (markReadOwnerReader) ListOwnedOrganizationIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (markReadOwnerReader) CountActiveOwnedOrganizations(_ context.Context, _ string) (int, error) {
	return 0, nil
}

// stubNotifWsReader resolves organization info with a fixed owner.
type stubNotifWsReader struct{}

func (stubNotifWsReader) GetOrganizationByID(_ context.Context, _ string) (*contracts.OrganizationInfo, error) {
	return &contracts.OrganizationInfo{OwnerID: "notif-owner-sub", Locale: "id"}, nil
}
func (stubNotifWsReader) IsMember(_ context.Context, _, _ string) (bool, error) { return true, nil }
func (stubNotifWsReader) GetMemberRole(_ context.Context, _, _ string) (string, error) {
	return "member", nil
}
func (stubNotifWsReader) ListMemberAuthSubs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (stubNotifWsReader) GetFirstOrganizationIDForMember(_ context.Context, _ string) (string, error) {
	return "00000000-0000-0000-0000-000000000e08", nil
}
func (stubNotifWsReader) ListMembershipsForExport(_ context.Context, _ string) ([]contracts.OrgMembershipInfo, error) {
	return nil, nil
}
func (stubNotifWsReader) ListOwnedOrganizationIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (stubNotifWsReader) CountActiveOwnedOrganizations(_ context.Context, _ string) (int, error) {
	return 0, nil
}

// stubNotifUserReader resolves a user by auth_sub with a fixed email.
type stubNotifUserReader struct{}

func (stubNotifUserReader) GetUserByAuthSub(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return &contracts.UserInfo{Email: "owner@test.com"}, nil
}

func (stubNotifUserReader) GetUserByEmail(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, nil
}

func (stubNotifUserReader) GetUsersByAuthSubs(_ context.Context, _ []string) (map[string]contracts.UserInfo, error) {
	return nil, nil
}

func (stubNotifUserReader) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestIntegration_ResolveOwnerEmail_WithStubReaders(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000e07"
	t.Cleanup(func() { cleanupNotifByOrganization(pool, orgID) })

	// module with non-nil orgReader and userReader — exercises resolveOwnerEmail fully
	mod := notification.New(pool, nil, stubNotifWsReader{}, stubNotifUserReader{}, "", nil)

	// HandleSubscriptionRemind calls send (inserts row) then resolveOwnerEmail → sendEmail (nil mailer, no-op)
	body := encodeSubscriptionRemindForNotif("sub-e07", orgID)
	if err := mod.Worker.HandleSubscriptionRemind(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionRemind with stub readers: %v", err)
	}

	// notification row must be present (send succeeded)
	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1`, orgID,
	).Scan(&count)
	if count == 0 {
		t.Error("expected notification row after HandleSubscriptionRemind")
	}
}
