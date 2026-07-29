package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

// Module owns organizations, memberships, and outbound webhooks.
// Implements contracts.OrganizationReader so the organization middleware can
// resolve organization data without importing this package directly.
type Module struct {
	svc        *service
	pool       *pgxpool.Pool
	cacheInval contracts.OrganizationCacheInvalidator
	Worker     *WebhookWorker
}

func New(pool *pgxpool.Pool, pub messaging.EventPublisher) *Module {
	repo := &repository{}
	svc := &service{repo: repo, pool: pool, pub: pub}
	return &Module{
		svc:    svc,
		pool:   pool,
		Worker: &WebhookWorker{repo: repo, pool: pool, pub: pub, log: slog.Default()},
	}
}

// SuspendOrganization implements contracts.OrganizationSuspender.
func (m *Module) SuspendOrganization(ctx context.Context, organizationID, reason string) error {
	return m.svc.suspendOrganization(ctx, organizationID, reason)
}

// UnsuspendOrganization implements contracts.OrganizationSuspender.
func (m *Module) UnsuspendOrganization(ctx context.Context, organizationID string) error {
	return m.svc.unsuspendOrganization(ctx, organizationID)
}

// SetCacheInvalidator wires the cache invalidator after construction.
// Called from main.go after the cached reader is created. Also wired onto
// svc, not just the handler, so service-layer bulk operations that never
// go through this module's own HTTP handler (resolveDowngradeOverage,
// called cross-module from billing) can still invalidate a removed
// member's cached RBAC role immediately instead of leaving them with
// stale cached access for up to the cache's TTL.
func (m *Module) SetCacheInvalidator(inv contracts.OrganizationCacheInvalidator) {
	m.cacheInval = inv
	m.svc.cacheInval = inv
}

// SetBillingReader wires the billing reader after construction to enforce plan limits.
// Called from main.go after the billing module is created.
func (m *Module) SetBillingReader(br contracts.BillingReader) {
	m.svc.billingReader = br
}

// SetBillingWriter wires the billing writer after construction to record usage.
// Called from main.go after the billing module is created.
func (m *Module) SetBillingWriter(bw contracts.BillingWriter) {
	m.svc.billingWriter = bw
}

// SetStorageClient wires the storage client after construction.
// Called from main.go after the storage client is created. Also wired onto
// Worker, not just svc — HandleOrganizationDeleted needs it to purge a
// deleted organization's files, and runs in the worker process, which
// constructs its own storage client (see RunWorker) independently of the
// API's.
func (m *Module) SetStorageClient(s *storage.Client) {
	m.svc.store = s
	m.Worker.store = s
}

// SetCatalogReader wires the catalog reader after construction to validate
// an optional plan on organization creation. Called from main.go after the
// billing module is created (billing owns the catalog schema).
func (m *Module) SetCatalogReader(r contracts.CatalogReader) {
	m.svc.catalogReader = r
}

// SetUserReader wires the user reader after construction to resolve an
// inviter's email for GET /me/invitations. Called from main.go after the
// account module is created.
func (m *Module) SetUserReader(r contracts.UserReader) {
	m.svc.userReader = r
}

// RemoveAllMemberships implements contracts.OrganizationWriter — deletes
// every membership for a user across all organizations. Called by the
// account module's GDPR delete-account flow.
func (m *Module) RemoveAllMemberships(ctx context.Context, authSub string) error {
	return m.svc.removeAllMemberships(ctx, authSub)
}

// GetOrganizationByID implements contracts.OrganizationReader.
func (m *Module) GetOrganizationByID(ctx context.Context, organizationID string) (*contracts.OrganizationInfo, error) {
	t, err := m.svc.getOrganization(ctx, organizationID)
	if err != nil {
		return nil, fmt.Errorf("organization.GetOrganizationByID: %w", err)
	}
	var s struct {
		AllowedIPs []string `json:"allowed_ips"`
	}
	if len(t.Settings) > 0 {
		_ = json.Unmarshal(t.Settings, &s)
	}

	return &contracts.OrganizationInfo{
		ID:                t.ID,
		Slug:              t.Slug,
		Name:              t.Name,
		Status:            t.Status,
		OwnerID:           t.OwnerID,
		InviteCode:        t.InviteCode,
		InviteCodeEnabled: t.InviteCodeEnabled,
		Timezone:          t.Timezone,
		Locale:            t.Locale,
		CountryCode:       t.CountryCode,
		AllowedIPs:        s.AllowedIPs,
	}, nil
}

// IsMember implements contracts.OrganizationReader.
func (m *Module) IsMember(ctx context.Context, organizationID, authSub string) (bool, error) {
	role, err := m.svc.getMemberRole(ctx, organizationID, authSub)
	if err != nil {
		return false, nil
	}
	return role != "", nil
}

// GetMemberRole implements contracts.OrganizationReader.
func (m *Module) GetMemberRole(ctx context.Context, organizationID, authSub string) (string, error) {
	return m.svc.getMemberRole(ctx, organizationID, authSub)
}

// ListMemberAuthSubs implements contracts.OrganizationReader.
func (m *Module) ListMemberAuthSubs(ctx context.Context, organizationID string) ([]string, error) {
	return m.svc.repo.listMemberAuthSubs(ctx, m.pool, organizationID)
}

// GetFirstOrganizationIDForMember implements contracts.OrganizationReader.
func (m *Module) GetFirstOrganizationIDForMember(ctx context.Context, authSub string) (string, error) {
	id, err := m.svc.repo.findFirstOrganizationIDByMember(ctx, m.pool, authSub)
	if err != nil {
		// belonging to no organization is a valid, common state — not an error for callers
		return "", nil
	}
	return id, nil
}

// ListMembershipsForExport implements contracts.OrganizationReader. Called
// by the account module's GDPR data-export flow.
func (m *Module) ListMembershipsForExport(ctx context.Context, authSub string) ([]contracts.OrgMembershipInfo, error) {
	views, err := m.svc.listMembershipsForExport(ctx, authSub)
	if err != nil {
		return nil, fmt.Errorf("organization.ListMembershipsForExport: %w", err)
	}
	out := make([]contracts.OrgMembershipInfo, len(views))
	for i, v := range views {
		out[i] = contracts.OrgMembershipInfo{ID: v.ID, Slug: v.Slug, Name: v.Name, Role: v.Role, JoinedAt: v.JoinedAt}
	}
	return out, nil
}

// ListOwnedOrganizationIDs implements contracts.OrganizationReader. Called
// by billing's provisionSubscription trial-eligibility check.
func (m *Module) ListOwnedOrganizationIDs(ctx context.Context, authSub string) ([]string, error) {
	return m.svc.listOwnedOrganizationIDs(ctx, authSub)
}

// CountActiveOwnedOrganizations implements contracts.OrganizationReader.
// Called by the account module's GDPR delete-account gate.
func (m *Module) CountActiveOwnedOrganizations(ctx context.Context, authSub string) (int, error) {
	return m.svc.countActiveOwnedOrganizations(ctx, authSub)
}

// ResolveDowngradeOverage implements contracts.OrganizationCommander.
func (m *Module) ResolveDowngradeOverage(
	ctx context.Context, organizationID string,
	preferredMemberAuthSubs []string, memberLimit int,
	preferredFileIDs []string, storageLimitBytes int64,
	dryRun bool,
) (contracts.OverageResolution, error) {
	return m.svc.resolveDowngradeOverage(ctx, organizationID, preferredMemberAuthSubs, memberLimit, preferredFileIDs, storageLimitBytes, dryRun)
}

// CleanupExpiredInvitations deletes invitations past their expiry. Called by the worker ticker.
func (m *Module) CleanupExpiredInvitations(ctx context.Context) {
	_ = m.svc.repo.deleteExpiredInvitations(ctx, m.pool)
}

// Register mounts organization and member routes onto r.
//
//	POST   /organizations
//	GET    /organizations
//	GET    /organizations/:organizationID
//	PATCH  /organizations/:organizationID
//	DELETE /organizations/:organizationID
//
//	GET    /organizations/:organizationID/members
//	POST   /organizations/:organizationID/members
//	DELETE /organizations/:organizationID/members/:authSub
//	PATCH  /organizations/:organizationID/members/:authSub/role
//
//	GET    /organizations/join/preview — read-only organization + owner details before the caller commits to joining
//	POST   /organizations/join
//
//	GET    /me/invitations         — the caller's own pending invitations, across every organization
//	GET    /invitations/preview     — read-only invitation details before the caller commits to accepting
//	POST   /invitations/accept
//	POST   /invitations/decline     — deletes the invitation, notifies the original inviter in-app
//	POST   /invitations/request-new — notifies the original inviter, not the requester
//
// mfaGate is applied to organization deletion and ownership transfer — an
// aal2-only gate for users who have MFA enabled, see
// contracts.UserReader.IsMFAEnabled.
func (m *Module) Register(r *gin.RouterGroup, deps httpserver.RouteDeps) {
	h := &handler{svc: m.svc, pool: m.pool, cacheInval: m.cacheInval}
	ownerOnly := middleware.RequireRole(contracts.RoleOwner)
	adminUp := middleware.RequireRole(contracts.RoleOwner, contracts.RoleAdmin)

	ws := r.Group("/organizations")
	ws.Use(deps.Auth, deps.RateLimit)
	{
		ws.POST("", h.createOrganization)
		ws.GET("", h.listOrganizations)
		ws.GET("/join/preview", h.previewInviteCode)
		ws.POST("/join", h.joinByCode)

		scoped := ws.Group("/:organizationID")
		scoped.Use(deps.Org)
		{
			scoped.GET("", h.getOrganization)
			scoped.PATCH("", ownerOnly, h.updateOrganization)
			scoped.DELETE("", ownerOnly, deps.MFA, h.deleteOrganization)
			scoped.DELETE("/leave", h.leaveOrganization)
			scoped.POST("/transfer", ownerOnly, deps.MFA, h.transferOwnership)
			scoped.PATCH("/settings", ownerOnly, h.updateSettings)
			scoped.POST("/suspend", ownerOnly, deps.MFA, h.suspendOrganization)
			scoped.POST("/unsuspend", ownerOnly, deps.MFA, h.unsuspendOrganization)

			scoped.POST("/invite-code", ownerOnly, h.regenerateInviteCode)
			scoped.PATCH("/invite-code", ownerOnly, h.toggleInviteCode)

			scoped.GET("/members", h.listMembers)
			scoped.POST("/members", adminUp, h.addMember)
			scoped.POST("/members/import", adminUp, h.importMembers)
			scoped.DELETE("/members/:authSub", adminUp, h.removeMember)
			scoped.PATCH("/members/:authSub/role", adminUp, h.updateMemberRole)

			scoped.GET("/audit-log", adminUp, h.auditLog)
			scoped.GET("/audit-log/export", adminUp, h.exportAuditLog)

			scoped.GET("/invitations", adminUp, h.listInvitations)
			scoped.POST("/invitations", adminUp, h.createInvitation)
			scoped.DELETE("/invitations/:invitationID", adminUp, h.revokeInvitation)

			webhooks := scoped.Group("/webhooks")
			webhooks.Use(ownerOnly)
			{
				webhooks.POST("", h.createWebhook)
				webhooks.GET("", h.listWebhooks)
				webhooks.PATCH("/:webhookID", h.updateWebhook)
				webhooks.DELETE("/:webhookID", h.deleteWebhook)
				webhooks.POST("/:webhookID/rotate-secret", h.rotateWebhookSecret)
				webhooks.POST("/:webhookID/test-event", h.sendWebhookTestEvent)
				webhooks.GET("/:webhookID/deliveries", h.listWebhookDeliveries)
				webhooks.POST("/:webhookID/deliveries/:deliveryID/retry", h.retryWebhookDelivery)
				webhooks.POST("/:webhookID/deliveries/retry-failed", h.retryAllFailedWebhookDeliveries)
			}

			scoped.POST("/logo", adminUp, h.uploadLogo)

			files := scoped.Group("/files")
			files.Use(adminUp)
			{
				files.POST("", h.uploadFile)
				files.GET("", h.listFiles)
				files.GET("/trash", h.listTrash)
				files.GET("/:fileID/download", h.downloadFile)
				files.PATCH("/:fileID", h.moveFile)
				files.DELETE("/:fileID", h.deleteOrganizationFile)
				files.POST("/bulk-delete", h.bulkDeleteFiles)
				files.POST("/:fileID/restore", h.restoreFile)
				files.DELETE("/:fileID/permanent", h.purgeFile)

				folders := files.Group("/folders")
				{
					folders.POST("", h.createFolder)
					folders.GET("", h.listFolders)
					folders.PATCH("/:folderID", h.updateFolder)
					folders.DELETE("/:folderID", h.deleteFolder)
				}
			}
		}
	}

	invites := r.Group("/invitations")
	invites.Use(deps.Auth, deps.RateLimit)
	{
		invites.GET("/preview", h.previewInvitation)
		invites.POST("/accept", h.acceptInvitation)
		invites.POST("/decline", h.declineInvitation)
		invites.POST("/request-new", h.requestNewInvitation)
	}

	r.GET("/me/invitations", deps.Auth, deps.RateLimit, h.listMyInvitations)
}
