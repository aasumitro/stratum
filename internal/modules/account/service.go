package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/httpclient"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

type service struct {
	repo           *repository
	pool           *pgxpool.Pool
	pub            messaging.EventPublisher
	adminURL       string // Supabase auth/v1 admin base URL
	serviceRoleKey string // Supabase service_role key
	revokedNS      *cache.Namespace
	store          *storage.Client

	// Cross-schema writers for the GDPR delete-account flow — nil-safe,
	// wired via Set* after construction like every other optional dependency.
	orgWriter     contracts.OrganizationWriter
	notifWriter   contracts.NotificationWriter
	billingWriter contracts.BillingWriter

	// Cross-schema readers for the GDPR data-export flow — same nil-safe
	// Set* convention, but treated as required at export time: an unwired
	// reader fails the whole export rather than silently omitting a section
	// (see buildExportData).
	orgReader   contracts.OrganizationReader
	notifReader contracts.NotificationReader
}

func (s *service) upsertProfile(ctx context.Context, authSub, email, fullName, avatarURL string) (*userRecord, error) {
	u, err := s.repo.upsertUser(ctx, s.pool, authSub, email, fullName, avatarURL)
	if err != nil {
		return nil, apperr.Internal("PROFILE_SAVE_FAILED", "failed to save profile", err)
	}
	return u, nil
}

func (s *service) getProfile(ctx context.Context, authSub string) (*userRecord, error) {
	u, err := s.repo.findUserByAuthSub(ctx, s.pool, authSub)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("PROFILE_NOT_FOUND", "profile not found", err)
		}
		return nil, apperr.Internal("PROFILE_FETCH_FAILED", "failed to get profile", err)
	}
	return u, nil
}

func (s *service) listProfilesByAuthSubs(ctx context.Context, authSubs []string) ([]userRecord, error) {
	return s.repo.findUsersByAuthSubs(ctx, s.pool, authSubs)
}

func (s *service) getProfileByEmail(ctx context.Context, email string) (*userRecord, error) {
	u, err := s.repo.findUserByEmail(ctx, s.pool, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("PROFILE_NOT_FOUND", "profile not found", err)
		}
		return nil, apperr.Internal("PROFILE_FETCH_FAILED", "failed to get profile", err)
	}
	return u, nil
}

func (s *service) updateProfile(ctx context.Context, authSub, fullName, avatarURL string) (*userRecord, error) {
	u, err := s.repo.updateUser(ctx, s.pool, authSub, fullName, avatarURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("PROFILE_NOT_FOUND", "profile not found", err)
		}
		return nil, apperr.Internal("PROFILE_UPDATE_FAILED", "failed to update profile", err)
	}
	events.Publish(ctx, s.pub, events.ExchangeAccount, events.RoutingKeyUserUpdated, "account", "", events.UserUpdated{
		UserID:    u.ID,
		UpdatedAt: u.UpdatedAt,
	})
	return u, nil
}

func (s *service) updatePreferences(ctx context.Context, authSub string, prefs json.RawMessage) error {
	if err := s.repo.updatePreferences(ctx, s.pool, authSub, prefs); err != nil {
		return apperr.Internal("PREFERENCES_UPDATE_FAILED", "failed to update preferences", err)
	}
	return nil
}

// syncEmail updates the cached email from a verified Supabase auth.users
// webhook — the only path by which email can change, since PATCH /me
// deliberately excludes it. A user who hasn't completed onboarding yet has
// no account.users row; that's a no-op, not an error.
func (s *service) syncEmail(ctx context.Context, authSub, email string) error {
	u, oldEmail, err := s.repo.updateEmail(ctx, s.pool, authSub, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return apperr.Internal("EMAIL_SYNC_FAILED", "failed to sync email", err)
	}

	// Best-effort: the webhook route sits outside the HTTP audit middleware
	// (same as Stripe/Xendit), so this is the only place this change gets logged.
	if err := s.repo.insertEmailChangeAuditEvent(ctx, s.pool, authSub, oldEmail, email); err != nil {
		slog.WarnContext(ctx, "account.syncEmail: audit log insert failed", "auth_sub", authSub, "error", err)
	}

	events.Publish(ctx, s.pub, events.ExchangeAccount, events.RoutingKeyUserEmailChanged, "account", "", events.UserEmailChanged{
		UserID:    u.ID,
		AuthSub:   authSub,
		OldEmail:  oldEmail,
		NewEmail:  email,
		ChangedAt: u.UpdatedAt,
	})
	return nil
}

func (s *service) requestDeleteAccount(ctx context.Context, authSub string) (*taskRecord, error) {
	if s.orgReader == nil {
		return nil, apperr.Validation("ACCOUNT_DELETE_FAILED", "account.requestDeleteAccount: organization reader not wired")
	}
	owned, err := s.orgReader.CountActiveOwnedOrganizations(ctx, authSub)
	if err != nil {
		return nil, apperr.Internal("ACCOUNT_DELETE_FAILED", "failed to request account deletion", err)
	}
	if owned > 0 {
		return nil, apperr.Validation("ACCOUNT_DELETE_FAILED", "transfer or delete your organizations before deleting your account")
	}

	task, err := s.repo.insertTask(ctx, s.pool, authSub, "delete_account")
	if err != nil {
		return nil, apperr.Internal("ACCOUNT_DELETE_FAILED", "failed to request account deletion", err)
	}

	events.Publish(ctx, s.pub, events.ExchangeAccount, events.RoutingKeyUserDeleteRequest, "account", "",
		events.UserTaskRequest{TaskID: task.ID, AuthSub: authSub})

	return task, nil
}

func (s *service) requestExportData(ctx context.Context, authSub string) (*taskRecord, error) {
	task, err := s.repo.insertTask(ctx, s.pool, authSub, "export_data")
	if err != nil {
		return nil, apperr.Internal("EXPORT_FAILED", "failed to request data export", err)
	}

	events.Publish(ctx, s.pub, events.ExchangeAccount, events.RoutingKeyUserExportRequest, "account", "",
		events.UserTaskRequest{TaskID: task.ID, AuthSub: authSub})

	return task, nil
}

// executeDeleteAccount is called by the worker, not by HTTP handlers. It
// removes the user's data from every schema that references auth_sub.
// True cross-schema atomicity isn't available without 2PC once these steps
// go through module contracts rather than a shared transaction, so each
// step is best-effort: a failure is logged and recorded on the task result
// (failed_steps) rather than silently discarded, so a partial deletion is
// visible instead of silent.
func (s *service) executeDeleteAccount(ctx context.Context, taskID, authSub string) error {
	_ = s.repo.markTaskProcessing(ctx, s.pool, taskID)

	steps := []struct {
		name string
		run  func() error
	}{
		{"notifications", func() error {
			if s.notifWriter == nil {
				return errors.New("notification writer not wired")
			}
			return s.notifWriter.DeleteAllForUser(ctx, authSub)
		}},
		{"memberships", func() error {
			if s.orgWriter == nil {
				return errors.New("organization writer not wired")
			}
			return s.orgWriter.RemoveAllMemberships(ctx, authSub)
		}},
		{"audit_log", func() error {
			return audit.AnonymizeActor(ctx, s.pool, authSub)
		}},
		{"billing_history", func() error {
			if s.billingWriter == nil {
				return errors.New("billing writer not wired")
			}
			return s.billingWriter.AnonymizeHistory(ctx, authSub)
		}},
		{"login_events", func() error {
			return s.repo.deleteLoginEvents(ctx, s.pool, authSub)
		}},
	}

	var failedSteps []string
	for _, step := range steps {
		if err := step.run(); err != nil {
			slog.ErrorContext(ctx, "account.executeDeleteAccount: cleanup step failed",
				"auth_sub", authSub, "step", step.name, "error", err)
			failedSteps = append(failedSteps, step.name)
		}
	}

	// Best-effort: wipe avatar blob before removing the DB record.
	if s.store != nil {
		_ = s.store.Delete(ctx, "users", authSub+"/avatar")
	}

	if err := s.repo.deleteUser(ctx, s.pool, authSub); err != nil {
		_ = s.repo.failTask(ctx, s.pool, taskID, err.Error())
		return fmt.Errorf("account.executeDeleteAccount: %w", err)
	}

	// Best-effort: remove Supabase auth user so they cannot log back in.
	// A failure here is logged but does not roll back the already-completed DB deletion.
	if s.adminURL != "" && s.serviceRoleKey != "" {
		if err := s.deleteSupabaseUser(ctx, authSub); err != nil {
			slog.WarnContext(ctx, "account.executeDeleteAccount: supabase user deletion failed",
				"auth_sub", authSub, "error", err)
		}
	}

	result := struct {
		Deleted     bool     `json:"deleted"`
		FailedSteps []string `json:"failed_steps,omitempty"`
	}{Deleted: true, FailedSteps: failedSteps}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("account.executeDeleteAccount: marshal result: %w", err)
	}

	return s.repo.completeTask(ctx, s.pool, taskID, resultJSON)
}

// exportEventLimit caps the notifications/audit-events sections of a GDPR
// export at the most recent N rows — matches the pre-existing LIMIT this
// flow has always used.
const exportEventLimit = 1000

// exportData is the GDPR data-export payload. Organizations/Notifications
// route through contracts readers (owned by their respective modules);
// AuditEvents comes directly from internal/platform/audit (a platform
// package, not a module — same direct-call precedent as audit.AnonymizeActor
// in the delete-account flow).
type exportData struct {
	Profile       *userRecord                   `json:"profile"`
	Organizations []contracts.OrgMembershipInfo `json:"organizations"`
	Notifications []contracts.MessageInfo       `json:"notifications"`
	AuditEvents   []exportAuditEvent            `json:"audit_events"`
}

// exportAuditEvent is the audit.events projection this export has always
// exposed — narrower than audit.EventRecord (no actor/metadata/ip/user_agent).
type exportAuditEvent struct {
	ID             string    `json:"id"`
	OrganizationID *string   `json:"organization_id,omitempty"`
	Action         string    `json:"action"`
	Resource       string    `json:"resource"`
	StatusCode     int       `json:"status_code"`
	CreatedAt      time.Time `json:"created_at"`
}

// buildExportData assembles the GDPR export from every schema that
// references auth_sub, each through the same contracts.* readers used
// elsewhere in this codebase — no more raw cross-schema SQL living in
// account's own repository. A failure in any section fails the whole
// export (rather than silently omitting it) so the caller never receives
// data they might mistake for complete.
func (s *service) buildExportData(ctx context.Context, authSub string) (*exportData, error) {
	profile, err := s.repo.findUserByAuthSub(ctx, s.pool, authSub)
	if err != nil {
		return nil, fmt.Errorf("account.buildExportData: profile: %w", err)
	}

	if s.orgReader == nil {
		return nil, errors.New("account.buildExportData: organization reader not wired")
	}
	orgs, err := s.orgReader.ListMembershipsForExport(ctx, authSub)
	if err != nil {
		return nil, fmt.Errorf("account.buildExportData: organizations: %w", err)
	}

	if s.notifReader == nil {
		return nil, errors.New("account.buildExportData: notification reader not wired")
	}
	notifs, err := s.notifReader.ListForUser(ctx, authSub, exportEventLimit)
	if err != nil {
		return nil, fmt.Errorf("account.buildExportData: notifications: %w", err)
	}

	records, _, err := audit.ListByActorCursor(ctx, s.pool, authSub, exportEventLimit, "", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("account.buildExportData: audit events: %w", err)
	}
	auditEvents := make([]exportAuditEvent, len(records))
	for i, e := range records {
		auditEvents[i] = exportAuditEvent{
			ID: e.ID, OrganizationID: e.OrganizationID, Action: e.Action,
			Resource: e.Resource, StatusCode: e.StatusCode, CreatedAt: e.CreatedAt,
		}
	}

	return &exportData{
		Profile:       profile,
		Organizations: orgs,
		Notifications: notifs,
		AuditEvents:   auditEvents,
	}, nil
}

// executeExportData is called by the worker, not by HTTP handlers.
func (s *service) executeExportData(ctx context.Context, taskID, authSub string) error {
	_ = s.repo.markTaskProcessing(ctx, s.pool, taskID)

	data, err := s.buildExportData(ctx, authSub)
	if err != nil {
		_ = s.repo.failTask(ctx, s.pool, taskID, err.Error())
		return fmt.Errorf("account.executeExportData: %w", err)
	}

	result, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("account.executeExportData: marshal result: %w", err)
	}
	return s.repo.completeTask(ctx, s.pool, taskID, result)
}

// supabaseAdminRequest builds and executes an authenticated request against
// the Supabase Admin API (Authorization + apikey headers), checking the
// status code. The caller is responsible for closing/decoding the body. op
// prefixes wrapped errors, e.g. "account.deleteSupabaseUser".
func (s *service) supabaseAdminRequest(ctx context.Context, op, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.adminURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", op, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.serviceRoleKey)
	req.Header.Set("apikey", s.serviceRoleKey)

	resp, err := httpclient.TrustedClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: http: %w", op, err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: status %d", op, resp.StatusCode)
	}
	return resp, nil
}

// deleteSupabaseUser calls the Supabase Admin API to remove the auth user,
// preventing the deleted user from signing in again with the same credentials.
func (s *service) deleteSupabaseUser(ctx context.Context, authSub string) error {
	resp, err := s.supabaseAdminRequest(ctx, "account.deleteSupabaseUser", http.MethodDelete, "/admin/users/"+authSub)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

type supabaseFactor struct {
	Status string `json:"status"`
}

type supabaseAdminUser struct {
	Factors []supabaseFactor `json:"factors"`
}

// isMFAEnabled reports the locally cached MFA state — cheap enough to call
// from a middleware on every request to a gated route. A caller with no
// account.users row yet is definitively not MFA-enabled (there's no
// enrollment for a profile that doesn't exist) rather than an ambiguous
// lookup failure, so ErrNoRows resolves to (false, nil) — the middleware
// fails closed (blocks) on a real error, but that's not what this is.
func (s *service) isMFAEnabled(ctx context.Context, authSub string) (bool, error) {
	enabled, err := s.repo.isMFAEnabled(ctx, s.pool, authSub)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// syncMFAStatus asks Supabase's Admin API for the authoritative list of the
// caller's MFA factors and persists whether any are verified. Never trust a
// client-supplied boolean for this — it gates aal2-only routes, so it must
// reflect Supabase's own state, not whatever the frontend claims.
func (s *service) syncMFAStatus(ctx context.Context, authSub string) (bool, error) {
	enabled, err := s.fetchAndPersistMFAStatus(ctx, authSub)
	if err != nil {
		return false, apperr.Internal("MFA_SYNC_FAILED", "failed to sync MFA status", err)
	}
	return enabled, nil
}

func (s *service) fetchAndPersistMFAStatus(ctx context.Context, authSub string) (bool, error) {
	if s.adminURL == "" || s.serviceRoleKey == "" {
		return false, fmt.Errorf("account.syncMFAStatus: supabase admin not configured")
	}

	resp, err := s.supabaseAdminRequest(ctx, "account.syncMFAStatus", http.MethodGet, "/admin/users/"+authSub)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var user supabaseAdminUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return false, fmt.Errorf("account.syncMFAStatus: decode: %w", err)
	}

	enabled := false
	for _, f := range user.Factors {
		if f.Status == "verified" {
			enabled = true
			break
		}
	}

	if err := s.repo.setMFAEnabled(ctx, s.pool, authSub, enabled); err != nil {
		return false, fmt.Errorf("account.syncMFAStatus: %w", err)
	}
	return enabled, nil
}

func (s *service) listTasks(ctx context.Context, authSub string) ([]taskRecord, error) {
	tasks, err := s.repo.listTasks(ctx, s.pool, authSub)
	if err != nil {
		return nil, apperr.Internal("TASKS_FETCH_FAILED", "failed to list tasks", err)
	}
	return tasks, nil
}

// getTask maps any lookup failure (not found or otherwise) to 404 — matches
// the pre-migration handler, which didn't distinguish ErrNoRows from other errors here.
func (s *service) getTask(ctx context.Context, id, authSub string) (*taskRecord, error) {
	task, err := s.repo.findTask(ctx, s.pool, id, authSub)
	if err != nil {
		return nil, apperr.NotFound("TASK_NOT_FOUND", "task not found", err)
	}
	return task, nil
}

// revokeAllSessions invalidates Supabase refresh tokens (blocking future
// token refreshes) and blocks the current access token via a Redis key
// (keyed by the caller's session_id claim — see middleware.SessionIdentifier)
// that the auth middleware checks on every request, so the already-issued
// access token stops working immediately rather than staying valid until
// its natural expiry.
func (s *service) revokeAllSessions(ctx context.Context, authSub, sessionID string, exp time.Time) error {
	if s.adminURL != "" && s.serviceRoleKey != "" {
		if err := s.revokeSupabaseSessions(ctx, authSub); err != nil {
			slog.WarnContext(ctx, "account.revokeAllSessions: supabase revoke failed",
				"auth_sub", authSub, "error", err)
		}
	}
	if sessionID == "" {
		return nil
	}
	ttl := time.Until(exp)
	if ttl <= 0 {
		return nil
	}
	if err := s.revokedNS.Set(ctx, "revoked_tokens:"+sessionID, 1, ttl); err != nil {
		return apperr.Internal("REVOKE_FAILED", "failed to revoke sessions", err)
	}
	return nil
}

func (s *service) revokeSupabaseSessions(ctx context.Context, authSub string) error {
	resp, err := s.supabaseAdminRequest(ctx, "account.revokeSupabaseSessions", http.MethodPost, "/admin/users/"+authSub+"/logout?scope=global")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (s *service) listSessions(ctx context.Context, authSub, cursor string, limit int) ([]loginEventRecord, string, error) {
	records, next, err := s.repo.listLoginEvents(ctx, s.pool, authSub, cursor, limit)
	if err != nil {
		return nil, "", apperr.Internal("SESSIONS_FETCH_FAILED", "failed to list sessions", err)
	}
	return records, next, nil
}

// listAuditLog returns the caller's own audit trail across every organization
// they've acted in, newest first. Optional from/to bound created_at.
func (s *service) listAuditLog(ctx context.Context, authSub string, limit int, cursor string, from, to *time.Time) ([]audit.EventRecord, string, error) {
	records, next, err := audit.ListByActorCursor(ctx, s.pool, authSub, limit, cursor, from, to)
	if err != nil {
		return nil, "", apperr.Internal("AUDIT_LOG_FETCH_FAILED", "failed to list audit log", err)
	}
	return records, next, nil
}

// exportAuditLog streams the caller's own audit trail as CSV.
func (s *service) exportAuditLog(ctx context.Context, authSub string, from, to *time.Time, w io.Writer) error {
	return audit.ExportByActor(ctx, s.pool, authSub, from, to, w)
}

// recordLoginEvent inserts a login event, rate-gated to once per 30 minutes
// per user to avoid a DB write on every single authenticated request. The
// same gated window also refreshes mfa_enabled from Supabase — see
// syncMFAStatusOnLogin. Fails closed on a Redis error (skips the write
// entirely) rather than treating an unreadable gate as "not gated yet" —
// this hook runs on every authenticated request, so failing open during a
// Redis outage would turn a brief blip into a DB insert storm plus one
// Supabase Admin API call per request.
func (s *service) recordLoginEvent(ctx context.Context, authSub, ip, ua string) {
	if s.revokedNS == nil {
		return
	}
	gateKey := "login_gate:" + authSub
	exists, err := s.revokedNS.Exists(ctx, gateKey)
	if err != nil {
		slog.ErrorContext(ctx, "account.recordLoginEvent: rate-gate check failed, skipping to avoid DB/API spam", "error", err)
		return
	}
	if exists {
		return
	}
	if err := s.revokedNS.Set(ctx, gateKey, 1, 30*time.Minute); err != nil {
		slog.ErrorContext(ctx, "account.recordLoginEvent: rate-gate set failed, skipping to avoid DB/API spam", "error", err)
		return
	}
	_ = s.repo.insertLoginEvent(ctx, s.pool, authSub, ip, ua)
	s.syncMFAStatusOnLogin(ctx, authSub)
}

// syncMFAStatusOnLogin refreshes mfa_enabled from Supabase's authoritative
// state, instead of relying solely on the client calling POST /me/mfa/sync
// — closes the staleness window where a user who enrolled MFA (elsewhere,
// or whose client just never called that endpoint) would otherwise stay
// unprotected by RequireMFAIfEnabled indefinitely. Only called from
// recordLoginEvent, so it's naturally rate-gated to the same once-per-
// 30-minutes cadence, bounding Supabase Admin API load.
//
// Runs in its own goroutine with its own deadline, detached from ctx's
// short lifetime: this is invoked from the auth middleware's fire-and-
// forget hook, which enforces a 200ms budget meant for cheap local DB
// writes — a real Supabase Admin API round trip routinely exceeds that.
func (s *service) syncMFAStatusOnLogin(ctx context.Context, authSub string) {
	if s.adminURL == "" || s.serviceRoleKey == "" {
		return
	}
	go func() {
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := s.fetchAndPersistMFAStatus(syncCtx, authSub); err != nil {
			slog.WarnContext(ctx, "account.syncMFAStatusOnLogin: mfa status sync failed", "auth_sub", authSub, "error", err)
		}
	}()
}

func (s *service) uploadAvatar(ctx context.Context, authSub string, r io.Reader, _ int64, contentType string) (*userRecord, error) {
	if s.store == nil {
		return nil, apperr.Internal("AVATAR_UPLOAD_FAILED", "failed to upload avatar",
			fmt.Errorf("account.uploadAvatar: storage not configured"))
	}
	path := authSub + "/avatar"
	if err := s.store.Upload(ctx, "users", path, r, contentType); err != nil {
		return nil, apperr.Internal("AVATAR_UPLOAD_FAILED", "failed to upload avatar",
			fmt.Errorf("account.uploadAvatar: %w", err))
	}
	publicURL := fmt.Sprintf("%s/storage/v1/object/public/users/%s", strings.TrimSuffix(s.store.BaseURL(), "/"), path)
	u, err := s.repo.updateAvatarURL(ctx, s.pool, authSub, publicURL)
	if err != nil {
		return nil, apperr.Internal("AVATAR_UPLOAD_FAILED", "failed to upload avatar", err)
	}
	return u, nil
}

func (s *service) deleteAvatar(ctx context.Context, authSub string) (*userRecord, error) {
	if s.store != nil {
		_ = s.store.Delete(ctx, "users", authSub+"/avatar")
	}
	u, err := s.repo.updateAvatarURL(ctx, s.pool, authSub, "")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("PROFILE_NOT_FOUND", "profile not found", err)
		}
		return nil, apperr.Internal("AVATAR_DELETE_FAILED", "failed to delete avatar", err)
	}
	return u, nil
}
