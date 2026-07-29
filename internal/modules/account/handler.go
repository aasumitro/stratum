package account

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

type handler struct {
	svc           *service
	webhookSecret string // SUPABASE_WEBHOOK_SECRET; empty = skip verification
}

// upsertProfile godoc
// @Summary      Create or update the caller's profile
// @Description  Upserts the authenticated user's profile row (full name, avatar URL); creates it on first call. Email is always taken from the caller's verified JWT claim, never the request body.
// @Tags         account
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      upsertProfileRequest                 true  "Profile fields"
// @Success      200   {object}  response.Payload{data=userRecord}
// @Failure      422   {object}  response.Payload  "validation failed, or the caller's token has no verified email"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /me [post]
func (h *handler) upsertProfile(c *gin.Context) {
	var req upsertProfileRequest
	if !request.Bind(c, &req) {
		return
	}

	email := reqctx.Email(c)
	if email == "" {
		response.Error("EMAIL_REQUIRED", "your account has no verified email").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	u, err := h.svc.upsertProfile(c.Request.Context(), reqctx.Subject(c), email, req.FullName, req.AvatarURL)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(u).JSON(c, http.StatusOK)
}

// getProfile godoc
// @Summary      Get the caller's profile
// @Description  Returns the authenticated user's profile row.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=userRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me [get]
func (h *handler) getProfile(c *gin.Context) {
	u, err := h.svc.getProfile(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(u).JSON(c, http.StatusOK)
}

// updateProfile godoc
// @Summary      Update the caller's profile
// @Description  Partially updates the authenticated user's full name and/or avatar URL. Email is not updatable here — see the Supabase user-updated webhook.
// @Tags         account
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      updateProfileRequest              true  "Fields to update"
// @Success      200   {object}  response.Payload{data=userRecord}
// @Failure      422   {object}  response.Payload  "validation failed"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /me [patch]
func (h *handler) updateProfile(c *gin.Context) {
	var req updateProfileRequest
	if !request.Bind(c, &req) {
		return
	}

	u, err := h.svc.updateProfile(c.Request.Context(), reqctx.Subject(c), req.FullName, req.AvatarURL)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(u).JSON(c, http.StatusOK)
}

// updatePreferences godoc
// @Summary      Merge into the caller's preferences
// @Description  Shallow-merges the given JSON object into the authenticated user's opaque preferences (e.g. {"lang":"en"}) — keys not in the body are left untouched. Preferences holds several independently-saved settings, each written by its own single-key PATCH, so merge (not replace) keeps one update from wiping out the others.
// @Tags         account
// @Accept       json
// @Security     BearerAuth
// @Param        body  body  object  true  "Arbitrary JSON object"
// @Success      204   "no content"
// @Failure      422   {object}  response.Payload  "body is not a JSON object"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/preferences [patch]
func (h *handler) updatePreferences(c *gin.Context) {
	var prefs json.RawMessage
	if err := c.ShouldBindJSON(&prefs); err != nil {
		request.ValidationError(err).JSON(c, http.StatusUnprocessableEntity)
		return
	}
	if len(prefs) == 0 || prefs[0] != '{' {
		response.Error("VALIDATION_FAILED", "preferences must be a JSON object").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	if err := h.svc.updatePreferences(c.Request.Context(), reqctx.Subject(c), prefs); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// deleteAccount godoc
// @Summary      Request account deletion
// @Description  Queues an async GDPR account-deletion task. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Success      202  {object}  response.Payload{data=taskRecord}
// @Failure      403  {object}  response.Payload  "MFA (aal2) step-up required"
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me [delete]
func (h *handler) deleteAccount(c *gin.Context) {
	task, err := h.svc.requestDeleteAccount(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(task).JSON(c, http.StatusAccepted)
}

// exportData godoc
// @Summary      Request a data export
// @Description  Queues an async GDPR data-export task (profile, memberships, notifications, audit log).
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Success      202  {object}  response.Payload{data=taskRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/export [post]
func (h *handler) exportData(c *gin.Context) {
	task, err := h.svc.requestExportData(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(task).JSON(c, http.StatusAccepted)
}

// listTasks godoc
// @Summary      List the caller's async tasks
// @Description  Returns all GDPR delete/export tasks requested by the authenticated user.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]taskRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/tasks [get]
func (h *handler) listTasks(c *gin.Context) {
	tasks, err := h.svc.listTasks(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(tasks, int64(len(tasks))).JSON(c, http.StatusOK)
}

// getTask godoc
// @Summary      Get a task by ID
// @Description  Returns a single GDPR delete/export task belonging to the authenticated user.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Param        taskID  path      string  true  "Task ID"
// @Success      200     {object}  response.Payload{data=taskRecord}
// @Failure      404     {object}  response.Payload  "task not found"
// @Failure      401     {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/tasks/{taskID} [get]
func (h *handler) getTask(c *gin.Context) {
	task, err := h.svc.getTask(c.Request.Context(), c.Param("taskID"), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(task).JSON(c, http.StatusOK)
}

// revokeAllSessions godoc
// @Summary      Revoke all sessions
// @Description  Revokes every active session for the caller, including the one making this request — a global sign-out. The caller will need to log in again.
// @Tags         account
// @Security     BearerAuth
// @Success      204  "no content"
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/sessions/revoke-all [post]
func (h *handler) revokeAllSessions(c *gin.Context) {
	claims, _ := middleware.ClaimsFromContext(c)

	sessionID := middleware.SessionIdentifier(claims.Raw)
	expRaw, _ := claims.Raw["exp"].(float64)
	exp := time.Unix(int64(expRaw), 0)

	if err := h.svc.revokeAllSessions(c.Request.Context(), claims.Subject, sessionID, exp); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// syncMFAStatus godoc
// @Summary      Sync MFA status
// @Description  Re-fetches MFA enrollment status from Supabase and persists it.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=object{mfa_enabled=boolean}}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/mfa/sync [post]
func (h *handler) syncMFAStatus(c *gin.Context) {
	enabled, err := h.svc.syncMFAStatus(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(map[string]bool{"mfa_enabled": enabled}).JSON(c, http.StatusOK)
}

// recordPasswordChanged exists solely to give a password change an audit
// trail entry. Password change itself is entirely client-side against
// Supabase (supabase.auth.updateUser({password})) — we never see, store, or
// need the password value. The generic audit middleware auto-logs any
// non-GET /api/v1 request, so this handler does no work beyond existing;
// there is nothing password-related to sync or persist.
//
// A body is explicitly rejected rather than silently accepted: the frontend
// contract is to call this with nothing after a successful password change,
// and this must never become a place a password (or anything else sensitive)
// could be sent and end up captured in audit.events.metadata.
//
// @Summary      Record a password change
// @Description  No-op endpoint that exists solely to give a client-side Supabase password change an audit trail entry. Must be called with an empty body.
// @Tags         account
// @Security     BearerAuth
// @Success      204  "no content"
// @Failure      400  {object}  response.Payload  "request body not allowed"
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/password/changed [post]
func (h *handler) recordPasswordChanged(c *gin.Context) {
	// Read the body directly rather than trusting Content-Length: a chunked
	// request (no Content-Length header) reports ContentLength == -1, which
	// would slip past a length check while still carrying a real body.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil || len(body) > 0 {
		response.Error("UNEXPECTED_BODY", "this endpoint does not accept a request body").JSON(c, http.StatusBadRequest)
		return
	}
	c.Status(http.StatusNoContent)
}

// listSessions godoc
// @Summary      List login sessions
// @Description  Cursor-paginated login history for the caller.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int     false  "max results (default 50, max 50)"
// @Param        cursor  query     string  false  "pagination cursor from a previous response"
// @Success      200     {object}  response.Payload{data=[]loginEventRecord}
// @Failure      401     {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/sessions [get]
func (h *handler) listSessions(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 50 {
			limit = v
		}
	}
	cursor := c.Query("cursor")

	events, nextCursor, err := h.svc.listSessions(c.Request.Context(), reqctx.Subject(c), cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.CursorList(events, nextCursor).JSON(c, http.StatusOK)
}

// auditLog godoc
// @Summary      List audit log entries
// @Description  Cursor-paginated audit log entries for actions taken by or affecting the caller, optionally bounded by from/to timestamps.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int     false  "max results (default 20, max 100)"
// @Param        cursor  query     string  false  "pagination cursor from a previous response"
// @Param        from    query     string  false  "RFC3339 start timestamp"
// @Param        to      query     string  false  "RFC3339 end timestamp"
// @Success      200     {object}  response.Payload{data=[]audit.EventRecord}
// @Failure      401     {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/audit-log [get]
func (h *handler) auditLog(c *gin.Context) {
	limit := 20
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}

	var from, to *time.Time
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			from = &t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			to = &t
		}
	}

	events, nextCursor, err := h.svc.listAuditLog(c.Request.Context(), reqctx.Subject(c), limit, c.Query("cursor"), from, to)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.CursorList(events, nextCursor).JSON(c, http.StatusOK)
}

// exportAuditLog godoc
// @Summary      Export audit log as CSV
// @Description  Streams the caller's audit log as a CSV attachment, optionally bounded by from/to timestamps.
// @Tags         account
// @Produce      text/csv
// @Security     BearerAuth
// @Param        from  query     string  false  "RFC3339 start timestamp"
// @Param        to    query     string  false  "RFC3339 end timestamp"
// @Success      200   {file}    file
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/audit-log/export [get]
func (h *handler) exportAuditLog(c *gin.Context) {
	var from, to *time.Time
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			from = &t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			to = &t
		}
	}

	c.Set("audit.action", "audit_log.export")
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=audit-log.csv")

	if err := h.svc.exportAuditLog(c.Request.Context(), reqctx.Subject(c), from, to, c.Writer); err != nil {
		c.Status(http.StatusInternalServerError)
	}
}

const maxAvatarSize = 2 << 20 // 2 MB

// uploadAvatar godoc
// @Summary      Upload an avatar
// @Description  Uploads and sets the caller's avatar image (max 2MB, image/* content type).
// @Tags         account
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        avatar  formData  file      true  "Avatar image file"
// @Success      200     {object}  response.Payload{data=object{avatar_url=string}}
// @Failure      422     {object}  response.Payload  "missing file, too large, or not an image"
// @Failure      401     {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/avatar [post]
func (h *handler) uploadAvatar(c *gin.Context) {
	file, header, err := c.Request.FormFile("avatar")
	if err != nil {
		response.Error("AVATAR_MISSING", "avatar file is required").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	defer file.Close()

	if header.Size > maxAvatarSize {
		response.Error("AVATAR_TOO_LARGE", "avatar must be under 2 MB").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	ct, ok := request.SniffImageType(file)
	if !ok {
		response.Error("AVATAR_INVALID_TYPE", "avatar must be an image").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	u, err := h.svc.uploadAvatar(c.Request.Context(), reqctx.Subject(c), file, header.Size, ct)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(map[string]string{"avatar_url": u.AvatarURL}).JSON(c, http.StatusOK)
}

// deleteAvatar godoc
// @Summary      Delete the caller's avatar
// @Description  Removes the caller's avatar image and clears avatar_url.
// @Tags         account
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=object{avatar_url=string}}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/avatar [delete]
func (h *handler) deleteAvatar(c *gin.Context) {
	u, err := h.svc.deleteAvatar(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(map[string]string{"avatar_url": u.AvatarURL}).JSON(c, http.StatusOK)
}

// supabaseUserWebhookPayload is the shape of a Supabase Database Webhook
// firing on auth.users UPDATE. Only the fields we care about are declared —
// this fires on every profile field, not just email.
type supabaseUserWebhookPayload struct {
	Table  string `json:"table"`
	Record struct {
		ID    string `json:"id"` // auth.users.id — matches account.users.auth_sub
		Email string `json:"email"`
	} `json:"record"`
	OldRecord struct {
		Email string `json:"email"`
	} `json:"old_record"`
}

// handleSupabaseUserUpdated syncs account.users.email after Supabase's own
// (Secure Email Change, double-confirmed) flow commits an email change.
// Every other field on auth.users is ignored — this is not a general
// profile sync.
//
// @Summary      Supabase user-updated webhook
// @Description  Internal webhook: syncs account.users.email after Supabase's own Secure Email Change flow commits a change. Verified via X-Webhook-Secret, not bearer auth.
// @Tags         webhooks
// @Accept       json
// @Param        X-Webhook-Secret  header  string                      true  "Shared webhook secret"
// @Param        body              body    supabaseUserWebhookPayload  true  "Supabase database webhook payload"
// @Success      200               "ok"
// @Failure      401               {object}  response.Payload  "invalid webhook secret"
// @Failure      400               {object}  response.Payload  "invalid payload"
// @Router       /webhooks/supabase/user-updated [post]
func (h *handler) handleSupabaseUserUpdated(c *gin.Context) {
	if !verifyWebhookSecret(c.GetHeader("X-Webhook-Secret"), h.webhookSecret) {
		response.Error("INVALID_SECRET", "invalid webhook secret").JSON(c, http.StatusUnauthorized)
		return
	}

	var payload supabaseUserWebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.Error("INVALID_PAYLOAD", "invalid payload").JSON(c, http.StatusBadRequest)
		return
	}

	if payload.Table != "users" || payload.Record.Email == "" || payload.Record.Email == payload.OldRecord.Email {
		c.Status(http.StatusOK)
		return
	}

	if err := h.svc.syncEmail(c.Request.Context(), payload.Record.ID, payload.Record.Email); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusOK)
}

// verifyWebhookSecret compares the X-Webhook-Secret header against the
// configured secret. An empty configSecret skips verification — only
// reachable in development, since config.RequireWebhookSecretsOutsideDev
// fails startup otherwise.
func verifyWebhookSecret(headerSecret, configSecret string) bool {
	if configSecret == "" {
		return true
	}
	return middleware.SecureCompare(headerSecret, configSecret)
}
