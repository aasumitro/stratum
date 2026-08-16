package organization_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/testsupport"
)

// orgBase returns the mount prefix for organizationID's scoped routes, as
// registered by organization.NewModuleEngine (module.go's Register mounts
// under /organizations on the /api group export_test.go's helpers build).
func orgBase(organizationID string) string {
	return "/api/organizations/" + organizationID
}

// seedOrgMember adds a second, non-owner member directly via SQL — used by
// the sub-resource cases below (removeMember/updateMemberRole), which need
// a real target auth_sub distinct from either org's owner.
func seedOrgMember(t *testing.T, pool *pgxpool.Pool, organizationID, authSub, role string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at)
		VALUES ($1, $2, $3, now())`, organizationID, authSub, role); err != nil {
		t.Fatalf("seed org member: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.memberships WHERE organization_id = $1 AND auth_sub = $2`, organizationID, authSub)
	})
}

// assertMembershipRole reads back a membership row's role, failing the test
// if the row is gone or the role changed — proves a cross-tenant call that
// returned a "success" status (204s that are really no-ops) didn't actually
// mutate the victim's data.
func assertMembershipRole(organizationID, authSub, wantRole string) func(t *testing.T, pool *pgxpool.Pool) {
	return func(t *testing.T, pool *pgxpool.Pool) {
		t.Helper()
		var role string
		err := pool.QueryRow(context.Background(),
			`SELECT role FROM organization.memberships WHERE organization_id = $1 AND auth_sub = $2`,
			organizationID, authSub,
		).Scan(&role)
		if err != nil {
			t.Fatalf("membership %s/%s: want to still exist, query failed: %v", organizationID, authSub, err)
		}
		if role != wantRole {
			t.Errorf("membership %s/%s: want role=%q (unchanged), got %q", organizationID, authSub, wantRole, role)
		}
	}
}

// TestCrossTenant_OrganizationManagement covers every organization/member/
// audit/invitation route not already covered by revokeInvitation's own
// dedicated cross-tenant test. Org A's owner must be denied acting on
// Org B's organization ID (membership-gate 403) or, for the two
// authSub-shaped sub-resource routes, on a member that belongs to Org B.
func TestCrossTenant_OrganizationManagement(t *testing.T) {
	pool := testPool(t)
	two := testsupport.SeedTwoOrgs(t, pool)
	memberB := "ct_member_b_" + two.OrgB
	seedOrgMember(t, pool, two.OrgB, memberB, "member")

	engine := organization.NewModuleEngine(pool, two.OwnerA)

	membershipOnly := []struct {
		name, method, path string
	}{
		{"updateOrganization", http.MethodPatch, orgBase(two.OrgB)},
		{"deleteOrganization", http.MethodDelete, orgBase(two.OrgB)},
		{"leaveOrganization", http.MethodDelete, orgBase(two.OrgB) + "/leave"},
		{"transferOwnership", http.MethodPost, orgBase(two.OrgB) + "/transfer"},
		{"updateSettings", http.MethodPatch, orgBase(two.OrgB) + "/settings"},
		{"suspendOrganization", http.MethodPost, orgBase(two.OrgB) + "/suspend"},
		{"unsuspendOrganization", http.MethodPost, orgBase(two.OrgB) + "/unsuspend"},
		{"regenerateInviteCode", http.MethodPost, orgBase(two.OrgB) + "/invite-code"},
		{"toggleInviteCode", http.MethodPatch, orgBase(two.OrgB) + "/invite-code"},
		{"listMembers", http.MethodGet, orgBase(two.OrgB) + "/members"},
		{"addMember", http.MethodPost, orgBase(two.OrgB) + "/members"},
		{"importMembers", http.MethodPost, orgBase(two.OrgB) + "/members/import"},
		{"removeMember_membershipOnly", http.MethodDelete, orgBase(two.OrgB) + "/members/" + memberB},
		{"updateMemberRole_membershipOnly", http.MethodPatch, orgBase(two.OrgB) + "/members/" + memberB + "/role"},
		{"auditLog", http.MethodGet, orgBase(two.OrgB) + "/audit-log"},
		{"exportAuditLog", http.MethodGet, orgBase(two.OrgB) + "/audit-log/export"},
		{"listInvitations", http.MethodGet, orgBase(two.OrgB) + "/invitations"},
		{"createInvitation", http.MethodPost, orgBase(two.OrgB) + "/invitations"},
		{"uploadLogo", http.MethodPost, orgBase(two.OrgB) + "/logo"},
	}

	cases := make([]testsupport.CrossTenantCase, 0, len(membershipOnly)+2)
	for _, r := range membershipOnly {
		cases = append(cases, testsupport.CrossTenantCase{
			Name: r.name, Method: r.method, Path: r.path, WantStatus: http.StatusForbidden,
		})
	}

	// Sub-resource cases: org A's own ID, but a member (authSub) that
	// belongs to org B. Both removeMember and updateMemberRole scope their
	// SQL by organization_id, so a member of a different org matches zero
	// rows — the repository layer reports that back and the service maps
	// it to 404, not a silent success. AssertNoMutation still proves org
	// B's membership survives untouched, independent of the status code.
	cases = append(cases,
		testsupport.CrossTenantCase{
			Name: "removeMember_crossOrgAuthSub", Method: http.MethodDelete,
			Path: orgBase(two.OrgA) + "/members/" + memberB, WantStatus: http.StatusNotFound,
			AssertNoMutation: assertMembershipRole(two.OrgB, memberB, "member"),
		},
		testsupport.CrossTenantCase{
			Name: "updateMemberRole_crossOrgAuthSub", Method: http.MethodPatch,
			Path: orgBase(two.OrgA) + "/members/" + memberB + "/role", Body: `{"role":"admin"}`,
			WantStatus:       http.StatusNotFound,
			AssertNoMutation: assertMembershipRole(two.OrgB, memberB, "member"),
		},
	)

	testsupport.RunCrossTenantCases(t, engine, pool, two.OwnerA, cases)
}

// seedWebhookEndpoint inserts an organization.webhook_endpoints row directly
// via SQL (columns per db/migrations/000002_organization.up.sql) and
// registers its own cleanup.
func seedWebhookEndpoint(t *testing.T, pool *pgxpool.Pool, organizationID, url string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO organization.webhook_endpoints (organization_id, url, secret_encrypted)
		VALUES ($1, $2, pgp_sym_encrypt('cross-tenant-test-secret', $3)) RETURNING id`,
		organizationID, url, testWebhookEncryptionKey,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed webhook endpoint: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.webhook_endpoints WHERE id = $1`, id)
	})
	return id
}

// assertWebhookEndpointExists fails the test if endpointID's row is gone —
// proves a cross-tenant call that returned a "success" status (204s that
// are really no-ops, see deleteWebhook below) didn't actually delete it.
func assertWebhookEndpointExists(endpointID string) func(t *testing.T, pool *pgxpool.Pool) {
	return func(t *testing.T, pool *pgxpool.Pool) {
		t.Helper()
		var exists bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS(SELECT 1 FROM organization.webhook_endpoints WHERE id = $1)`, endpointID,
		).Scan(&exists); err != nil {
			t.Fatalf("query webhook endpoint existence: %v", err)
		}
		if !exists {
			t.Errorf("webhook endpoint %s: want to still exist, but it was deleted", endpointID)
		}
	}
}

// TestCrossTenant_Webhooks covers organization's webhook-management routes.
// The two membership-only routes (create/list) get the standard 403 case;
// the seven :webhookID sub-resource routes each also get a case where org
// A's owner calls with org A's own ID but org B's webhook ID — every one
// of them now reports 404, uniformly, whether the not-found comes from a
// SELECT (findWebhookEndpoint), an UPDATE...RETURNING, or a scoped DELETE
// that matched zero rows. AssertNoMutation on deleteWebhook still proves
// org B's row survives, independent of the status code.
func TestCrossTenant_Webhooks(t *testing.T) {
	pool := testPool(t)
	two := testsupport.SeedTwoOrgs(t, pool)
	webhookB := seedWebhookEndpoint(t, pool, two.OrgB, "https://example.com/hook")

	engine := organization.NewModuleEngine(pool, two.OwnerA)
	webhooksBaseB := orgBase(two.OrgB) + "/webhooks"
	webhooksBaseA := orgBase(two.OrgA) + "/webhooks"

	cases := []testsupport.CrossTenantCase{
		{Name: "createWebhook", Method: http.MethodPost, Path: webhooksBaseB, Body: `{"url":"https://example.com/hook"}`, WantStatus: http.StatusForbidden},
		{Name: "listWebhooks", Method: http.MethodGet, Path: webhooksBaseB, WantStatus: http.StatusForbidden},

		// enabled:false deliberately, not true — enabled:true would route
		// through the service's own pre-check (findWebhookEndpoint, already
		// exercised by the sub-resource cases below) before ever reaching
		// this UPDATE, masking whether the UPDATE's own WHERE clause is
		// independently scoped.
		{Name: "updateWebhook_crossOrgWebhookID", Method: http.MethodPatch, Path: webhooksBaseA + "/" + webhookB,
			Body: `{"url":"https://example.com/other","enabled":false}`, WantStatus: http.StatusNotFound},
		{Name: "rotateWebhookSecret_crossOrgWebhookID", Method: http.MethodPost, Path: webhooksBaseA + "/" + webhookB + "/rotate-secret",
			WantStatus: http.StatusNotFound},
		{Name: "sendWebhookTestEvent_crossOrgWebhookID", Method: http.MethodPost, Path: webhooksBaseA + "/" + webhookB + "/test-event",
			WantStatus: http.StatusNotFound},
		{Name: "listWebhookDeliveries_crossOrgWebhookID", Method: http.MethodGet, Path: webhooksBaseA + "/" + webhookB + "/deliveries",
			WantStatus: http.StatusNotFound},
		{Name: "retryWebhookDelivery_crossOrgWebhookID", Method: http.MethodPost,
			Path:       webhooksBaseA + "/" + webhookB + "/deliveries/00000000-0000-0000-0000-000000000000/retry",
			WantStatus: http.StatusNotFound},
		{Name: "retryAllFailedWebhookDeliveries_crossOrgWebhookID", Method: http.MethodPost,
			Path: webhooksBaseA + "/" + webhookB + "/deliveries/retry-failed", WantStatus: http.StatusNotFound},
		{Name: "deleteWebhook_crossOrgWebhookID", Method: http.MethodDelete, Path: webhooksBaseA + "/" + webhookB,
			WantStatus: http.StatusNotFound, AssertNoMutation: assertWebhookEndpointExists(webhookB)},
	}

	testsupport.RunCrossTenantCases(t, engine, pool, two.OwnerA, cases)
}
