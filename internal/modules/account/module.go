package account

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

// Module owns the user's account data — profile fields, async GDPR tasks,
// and login history.
// Implements contracts.UserReader so other modules can resolve a user
// by auth_sub without importing this package directly.
type Module struct {
	svc           *service
	Worker        *Worker
	webhookSecret string
}

func New(
	pool *pgxpool.Pool,
	pub messaging.EventPublisher,
	adminURL, serviceRoleKey string,
	revokedNS *cache.Namespace,
	store *storage.Client,
	webhookSecret string,
) *Module {
	svc := &service{
		repo:           &repository{},
		pool:           pool,
		pub:            pub,
		adminURL:       adminURL,
		serviceRoleKey: serviceRoleKey,
		revokedNS:      revokedNS,
		store:          store,
	}
	return &Module{svc: svc, Worker: &Worker{svc: svc}, webhookSecret: webhookSecret}
}

// GetUserByAuthSub implements contracts.UserReader.
func (m *Module) GetUserByAuthSub(ctx context.Context, authSub string) (*contracts.UserInfo, error) {
	u, err := m.svc.getProfile(ctx, authSub)
	if err != nil {
		return nil, fmt.Errorf("account.GetUserByAuthSub: %w", err)
	}
	return &contracts.UserInfo{
		ID:        u.ID,
		AuthSub:   u.AuthSub,
		Email:     u.Email,
		Name:      u.FullName,
		AvatarURL: u.AvatarURL,
		Lang:      preferredLang(u.Preferences),
	}, nil
}

// GetUserByEmail implements contracts.UserReader.
func (m *Module) GetUserByEmail(ctx context.Context, email string) (*contracts.UserInfo, error) {
	u, err := m.svc.getProfileByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("account.GetUserByEmail: %w", err)
	}
	return &contracts.UserInfo{
		ID:        u.ID,
		AuthSub:   u.AuthSub,
		Email:     u.Email,
		Name:      u.FullName,
		AvatarURL: u.AvatarURL,
		Lang:      preferredLang(u.Preferences),
	}, nil
}

// GetUsersByAuthSubs implements contracts.UserReader.
func (m *Module) GetUsersByAuthSubs(ctx context.Context, authSubs []string) (map[string]contracts.UserInfo, error) {
	users, err := m.svc.listProfilesByAuthSubs(ctx, authSubs)
	if err != nil {
		return nil, fmt.Errorf("account.GetUsersByAuthSubs: %w", err)
	}
	out := make(map[string]contracts.UserInfo, len(users))
	for _, u := range users {
		out[u.AuthSub] = contracts.UserInfo{
			ID:        u.ID,
			AuthSub:   u.AuthSub,
			Email:     u.Email,
			Name:      u.FullName,
			AvatarURL: u.AvatarURL,
			Lang:      preferredLang(u.Preferences),
		}
	}
	return out, nil
}

// preferredLang extracts the "lang" key from a user's opaque preferences
// JSON (written via PATCH /me/preferences). Empty if unset or unparseable.
func preferredLang(prefs json.RawMessage) string {
	var v struct {
		Lang string `json:"lang"`
	}
	_ = json.Unmarshal(prefs, &v)
	return v.Lang
}

// IsMFAEnabled implements contracts.UserReader.
func (m *Module) IsMFAEnabled(ctx context.Context, authSub string) (bool, error) {
	return m.svc.isMFAEnabled(ctx, authSub)
}

// SetOrganizationWriter wires the organization writer after construction to
// remove a deleted user's memberships. Called from main.go after the
// organization module is created.
func (m *Module) SetOrganizationWriter(w contracts.OrganizationWriter) {
	m.svc.orgWriter = w
}

// SetNotificationWriter wires the notification writer after construction to
// delete a deleted user's messages. Called from main.go after the
// notification module is created.
func (m *Module) SetNotificationWriter(w contracts.NotificationWriter) {
	m.svc.notifWriter = w
}

// SetBillingWriter wires the billing writer after construction to anonymize
// a deleted user's subscription history. Called from main.go after the
// billing module is created.
func (m *Module) SetBillingWriter(w contracts.BillingWriter) {
	m.svc.billingWriter = w
}

// SetOrganizationReader wires the organization reader after construction to
// list a user's memberships for the GDPR data-export flow. Called from
// main.go after the organization module is created.
func (m *Module) SetOrganizationReader(r contracts.OrganizationReader) {
	m.svc.orgReader = r
}

// SetNotificationReader wires the notification reader after construction to
// list a user's notification history for the GDPR data-export flow. Called
// from main.go after the notification module is created.
func (m *Module) SetNotificationReader(r contracts.NotificationReader) {
	m.svc.notifReader = r
}

// HasStorage reports whether a storage client was wired at construction.
func (m *Module) HasStorage() bool { return m.svc.store != nil }

// MustBeWired panics if any of the five setters above were never called.
// Unlike every other module's optional cross-module dependencies (which are
// deliberately nil-safe and fail open so an unwired reader degrades to
// unlimited/access-granted/skip rather than blocking the request), these
// five are the documented exception: the GDPR delete
// and export flows (executeDeleteAccount, buildExportData) already fail
// the request/task outright if one is unwired, rather than silently
// producing incomplete data. Call once at startup, after every SetXxx
// call, so a missing wire is a boot-time panic instead of a failure a
// user only discovers by triggering account deletion or data export.
func (m *Module) MustBeWired() {
	switch {
	case m.svc.orgWriter == nil:
		panic("account.Module: organization writer not wired (call SetOrganizationWriter)")
	case m.svc.notifWriter == nil:
		panic("account.Module: notification writer not wired (call SetNotificationWriter)")
	case m.svc.billingWriter == nil:
		panic("account.Module: billing writer not wired (call SetBillingWriter)")
	case m.svc.orgReader == nil:
		panic("account.Module: organization reader not wired (call SetOrganizationReader)")
	case m.svc.notifReader == nil:
		panic("account.Module: notification reader not wired (call SetNotificationReader)")
	}
}

// MustHaveSessionRevocationWired panics if revokedNS was never wired. Call
// only from cmd/api's bootstrap, never cmd/worker's: cmd/worker
// intentionally passes a nil revokedNS to New (no HTTP auth path exists
// there to revoke sessions against), so calling this from worker_modules.go
// would panic on a correct, intentional configuration.
func (m *Module) MustHaveSessionRevocationWired() {
	if m.svc.revokedNS == nil {
		panic("account.Module: session revocation namespace not wired (revokedNS) — required for cmd/api only")
	}
}

// RecordLoginEvent rate-gates and records a login event for the auth middleware hook.
func (m *Module) RecordLoginEvent(ctx context.Context, authSub, ip, ua string) {
	m.svc.recordLoginEvent(ctx, authSub, ip, ua)
}

// Register mounts account routes onto r.
//
//	GET    /me
//	POST   /me
//	PATCH  /me
//	PATCH  /me/preferences
//	DELETE /me
//	POST   /me/export
//	GET    /me/tasks
//	GET    /me/tasks/:taskID
//	GET    /me/sessions
//	POST   /me/sessions/revoke-all
//	POST   /me/avatar
//	DELETE /me/avatar
//	POST   /me/mfa/sync
//	POST   /me/password/changed
//	GET    /me/audit-log
//	GET    /me/audit-log/export
//
// mfaGate is applied to account deletion — an aal2-only gate for users who
// have MFA enabled, see contracts.UserReader.IsMFAEnabled.
func (m *Module) Register(r *gin.RouterGroup, deps httpserver.RouteDeps) {
	h := &handler{svc: m.svc}

	me := r.Group("/me")
	me.Use(deps.Auth, deps.RateLimit)
	{
		me.GET("", h.getProfile)
		me.POST("", h.upsertProfile)
		me.PATCH("", h.updateProfile)
		me.PATCH("/preferences", h.updatePreferences)
		me.DELETE("", deps.MFA, h.deleteAccount)
		me.POST("/export", h.exportData)
		me.GET("/tasks", h.listTasks)
		me.GET("/tasks/:taskID", h.getTask)
		me.GET("/sessions", h.listSessions)
		me.POST("/sessions/revoke-all", h.revokeAllSessions)
		me.POST("/avatar", h.uploadAvatar)
		me.DELETE("/avatar", h.deleteAvatar)
		me.POST("/mfa/sync", h.syncMFAStatus)
		me.POST("/password/changed", h.recordPasswordChanged)
		me.GET("/audit-log", h.auditLog)
		me.GET("/audit-log/export", h.exportAuditLog)
	}
}

// RegisterWebhooks mounts public webhook endpoints — no auth middleware.
//
//	POST /webhooks/supabase/user-updated
//
// Supabase's own Auth flow verifies and commits an email change (with
// "Secure email change" requiring both old and new addresses to confirm);
// this only syncs the already-verified result into account.users.email,
// which PATCH /me deliberately never touches.
func (m *Module) RegisterWebhooks(r *gin.RouterGroup) {
	h := &handler{svc: m.svc, webhookSecret: m.webhookSecret}
	r.POST("/supabase/user-updated", h.handleSupabaseUserUpdated)
}
