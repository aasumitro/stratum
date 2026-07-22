package audit_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// seedEvent inserts an audit event with an explicit created_at so ordering
// across seeded rows is deterministic (INSERT ... DEFAULT now() can tie
// multiple rows to the same instant under -race).
func seedEvent(t *testing.T, pool *pgxpool.Pool, orgID *string, actor, action, resource string, createdAt time.Time) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO audit.events (organization_id, actor, action, resource, status_code, ip, user_agent, created_at)
		VALUES ($1, $2, $3, $4, 200, '', '', $5)`,
		orgID, actor, action, resource, createdAt,
	)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
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

func TestIntegration_AnonymousAuthFailureAuditEvent(t *testing.T) {
	pool := testPool(t)

	writer := audit.NewWriter(pool, slog.Default(), 90)
	defer writer.Stop()

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(writer.Middleware())
	e.POST("/audit-integ-test", func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/audit-integ-test", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}

	time.Sleep(700 * time.Millisecond) // wait for flush interval

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit.events WHERE resource = '/audit-integ-test'`)
	})

	var actor string
	var statusCode int
	err := pool.QueryRow(t.Context(), `
		SELECT actor, status_code FROM audit.events
		WHERE actor = 'anonymous' AND status_code = 401 AND resource = '/audit-integ-test'
		ORDER BY created_at DESC LIMIT 1`,
	).Scan(&actor, &statusCode)
	if err != nil {
		t.Fatalf("audit event not found: %v", err)
	}
	if actor != "anonymous" {
		t.Errorf("want actor=anonymous, got %s", actor)
	}
	if statusCode != http.StatusUnauthorized {
		t.Errorf("want status_code=401, got %d", statusCode)
	}
}

// TestIntegration_Writer_Stop_FlushesSynchronously regression-tests that
// Stop() actually blocks until the writer's final batch has been flushed —
// previously it only closed the channel and returned immediately, so a
// caller relying on the documented "waits for the flush to complete"
// behavior (e.g. shutdown code closing the DB pool right after Stop
// returns) could race the flush against the pool going away. No
// time.Sleep here — if Stop() doesn't truly block on the flush, this event
// won't exist yet when the test queries for it immediately after.
func TestIntegration_Writer_Stop_FlushesSynchronously(t *testing.T) {
	pool := testPool(t)

	writer := audit.NewWriter(pool, slog.Default(), 90)

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		// an authenticated, successful (2xx) request — an anonymous 2xx
		// is deliberately skipped by the middleware (probe/health traffic
		// filter), which would make this test pass for the wrong reason.
		c.Set("auth.claims", middleware.Claims{Subject: "sub_audit_stop_test"})
		c.Next()
	})
	e.Use(writer.Middleware())
	e.POST("/audit-stop-test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/audit-stop-test", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	writer.Stop() // must not return until the event above is durably flushed

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit.events WHERE resource = '/audit-stop-test'`)
	})

	var statusCode int
	err := pool.QueryRow(t.Context(), `
		SELECT status_code FROM audit.events
		WHERE resource = '/audit-stop-test'
		ORDER BY created_at DESC LIMIT 1`,
	).Scan(&statusCode)
	if err != nil {
		t.Fatalf("audit event not found immediately after Stop() returned: %v", err)
	}
	if statusCode != http.StatusOK {
		t.Errorf("want status_code=200, got %d", statusCode)
	}
}

func TestIntegration_SensitiveRequestBody_RedactedInAuditButNotForHandler(t *testing.T) {
	pool := testPool(t)

	writer := audit.NewWriter(pool, slog.Default(), 90)
	defer writer.Stop()

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		// authenticated, non-error requests are still audited — only
		// anonymous + non-error + no explicit action gets skipped
		c.Set("auth.claims", middleware.Claims{Subject: "redact_test_sub"})
		c.Next()
	})
	e.Use(writer.Middleware())

	// Echoes back exactly what it received — proves the handler still gets
	// the real, unredacted body even though the audit copy is scrubbed.
	var handlerSawPassword string
	e.POST("/audit-redact-test", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		var payload struct {
			Password string `json:"password"`
		}
		json.Unmarshal(body, &payload)
		handlerSawPassword = payload.Password
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/audit-redact-test",
		strings.NewReader(`{"password":"hunter2","full_name":"John"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if handlerSawPassword != "hunter2" {
		t.Fatalf("handler should still receive the real password value, got %q", handlerSawPassword)
	}

	time.Sleep(700 * time.Millisecond) // wait for flush interval

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit.events WHERE resource = '/audit-redact-test'`)
	})

	var metadata string
	err := pool.QueryRow(t.Context(), `
		SELECT metadata::text FROM audit.events
		WHERE resource = '/audit-redact-test' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&metadata)
	if err != nil {
		t.Fatalf("audit event not found: %v", err)
	}
	if strings.Contains(metadata, "hunter2") {
		t.Errorf("audit metadata must never contain the real password value, got %s", metadata)
	}
	if !strings.Contains(metadata, "[REDACTED]") {
		t.Errorf("audit metadata should show the redaction marker, got %s", metadata)
	}
	if !strings.Contains(metadata, "John") {
		t.Errorf("non-sensitive fields should still be visible in metadata, got %s", metadata)
	}
}

func TestIntegration_ListByOrganizationCursor_Filter(t *testing.T) {
	pool := testPool(t)
	orgID := "filter_test_org_" + t.Name()

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	rows := []struct{ actor, action, resource string }{
		{"filter_actor_a", "POST", "/organizations/:id/members"},
		{"filter_actor_a", "DELETE", "/organizations/:id/members/:authSub"},
		{"filter_actor_b", "POST", "/organizations/:id/invitations"},
	}
	for _, r := range rows {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO audit.events (organization_id, actor, action, resource, status_code, ip, user_agent)
			VALUES ($1, $2, $3, $4, 200, '', '')`,
			orgID, r.actor, r.action, r.resource,
		)
		if err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}

	// Actor filter (exact match).
	events, _, err := audit.ListByOrganizationCursor(t.Context(), pool, orgID, 20, "", audit.Filter{Actor: "filter_actor_a"})
	if err != nil {
		t.Fatalf("ListByOrganizationCursor: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("actor filter: want 2 events, got %d", len(events))
	}

	// Action filter (exact match).
	events, _, err = audit.ListByOrganizationCursor(t.Context(), pool, orgID, 20, "", audit.Filter{Action: "DELETE"})
	if err != nil {
		t.Fatalf("ListByOrganizationCursor: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("action filter: want 1 event, got %d", len(events))
	}

	// Resource filter (partial ILIKE match).
	events, _, err = audit.ListByOrganizationCursor(t.Context(), pool, orgID, 20, "", audit.Filter{Resource: "invitations"})
	if err != nil {
		t.Fatalf("ListByOrganizationCursor: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("resource filter: want 1 event, got %d", len(events))
	}

	// Combined actor+action filter narrows to the intersection.
	events, _, err = audit.ListByOrganizationCursor(t.Context(), pool, orgID, 20, "",
		audit.Filter{Actor: "filter_actor_a", Action: "POST"})
	if err != nil {
		t.Fatalf("ListByOrganizationCursor: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("combined filter: want 1 event, got %d", len(events))
	}

	// No filter returns everything for the org.
	events, _, err = audit.ListByOrganizationCursor(t.Context(), pool, orgID, 20, "", audit.Filter{})
	if err != nil {
		t.Fatalf("ListByOrganizationCursor: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("no filter: want 3 events, got %d", len(events))
	}
}

// TestIntegration_ListByOrganization_Filter regression-tests that the
// page-based listing (handler_audit.go's non-cursor branch) actually
// narrows by actor/action — ListByOrganization previously had no filter
// parameter at all, so the page-mode audit log silently ignored every
// chip filter while cursor mode and CSV export both applied them correctly.
func TestIntegration_ListByOrganization_Filter(t *testing.T) {
	pool := testPool(t)
	orgID := "page_filter_test_org_" + t.Name()

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	rows := []struct{ actor, action, resource string }{
		{"page_filter_actor_a", "POST", "/organizations/:id/members"},
		{"page_filter_actor_a", "DELETE", "/organizations/:id/members/:authSub"},
		{"page_filter_actor_b", "POST", "/organizations/:id/invitations"},
	}
	for _, r := range rows {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO audit.events (organization_id, actor, action, resource, status_code, ip, user_agent)
			VALUES ($1, $2, $3, $4, 200, '', '')`,
			orgID, r.actor, r.action, r.resource,
		)
		if err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}

	events, total, err := audit.ListByOrganization(t.Context(), pool, orgID, 20, 0, audit.Filter{Actor: "page_filter_actor_a"})
	if err != nil {
		t.Fatalf("ListByOrganization: %v", err)
	}
	if total != 2 {
		t.Errorf("actor filter: total = %d, want 2", total)
	}
	if len(events) != 2 {
		t.Errorf("actor filter: got %d events, want 2", len(events))
	}

	events, total, err = audit.ListByOrganization(t.Context(), pool, orgID, 20, 0, audit.Filter{Action: "DELETE"})
	if err != nil {
		t.Fatalf("ListByOrganization: %v", err)
	}
	if total != 1 || len(events) != 1 {
		t.Errorf("action filter: total = %d, got %d events, want 1 and 1", total, len(events))
	}
}

// TestIntegration_ListByOrganization_PageOffset covers the org admin audit
// log's classic page-based view (handler_audit.go's non-cursor branch,
// used when the UI requests a specific ?page=N rather than infinite scroll).
func TestIntegration_ListByOrganization_PageOffset(t *testing.T) {
	pool := testPool(t)
	orgID := "page_test_org_" + t.Name()

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	base := time.Now().UTC()
	// 5 events, newest (i=0) to oldest (i=4), one second apart.
	for i := range 5 {
		seedEvent(t, pool, &orgID, "page_actor", "POST", "/organizations/:id/members",
			base.Add(-time.Duration(i)*time.Second))
	}

	// Page 1 of 2 (limit=2): the two newest events.
	events, total, err := audit.ListByOrganization(t.Context(), pool, orgID, 2, 0, audit.Filter{})
	if err != nil {
		t.Fatalf("ListByOrganization page 1: %v", err)
	}
	if total != 5 {
		t.Errorf("total: want 5, got %d", total)
	}
	if len(events) != 2 {
		t.Fatalf("page 1: want 2 events, got %d", len(events))
	}
	if !events[0].CreatedAt.After(events[1].CreatedAt) {
		t.Errorf("page 1: want newest-first ordering, got %v then %v", events[0].CreatedAt, events[1].CreatedAt)
	}

	// Page 3 (offset=4): the single oldest remaining event.
	events, total, err = audit.ListByOrganization(t.Context(), pool, orgID, 2, 4, audit.Filter{})
	if err != nil {
		t.Fatalf("ListByOrganization page 3: %v", err)
	}
	if total != 5 {
		t.Errorf("total: want 5, got %d", total)
	}
	if len(events) != 1 {
		t.Fatalf("page 3: want 1 event, got %d", len(events))
	}
}

// TestIntegration_ExportByOrganization_CSV_HonorsFilter covers the org
// admin CSV export (handler_audit.go's exportAuditLog), which must match
// whatever filter the admin currently has applied on screen.
func TestIntegration_ExportByOrganization_CSV_HonorsFilter(t *testing.T) {
	pool := testPool(t)
	orgID := "export_org_test_" + t.Name()

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	now := time.Now().UTC()
	seedEvent(t, pool, &orgID, "export_actor_a", "POST", "/organizations/:id/members", now)
	seedEvent(t, pool, &orgID, "export_actor_a", "DELETE", "/organizations/:id/members/:authSub", now.Add(-time.Second))
	seedEvent(t, pool, &orgID, "export_actor_b", "POST", "/organizations/:id/invitations", now.Add(-2*time.Second))

	var buf bytes.Buffer
	if err := audit.ExportByOrganization(t.Context(), pool, orgID, audit.Filter{Actor: "export_actor_a"}, &buf); err != nil {
		t.Fatalf("ExportByOrganization: %v", err)
	}

	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	wantHeader := []string{"id", "organization_id", "actor", "action", "resource", "status_code", "ip", "user_agent", "created_at"}
	if len(rows) == 0 || len(rows[0]) != len(wantHeader) {
		t.Fatalf("header: want %v, got %v", wantHeader, rows[0])
	}
	dataRows := rows[1:]
	if len(dataRows) != 2 {
		t.Fatalf("want 2 filtered rows, got %d: %v", len(dataRows), dataRows)
	}
	for _, r := range dataRows {
		if r[2] != "export_actor_a" {
			t.Errorf("row actor: want export_actor_a, got %s", r[2])
		}
	}
}

// TestIntegration_ExportByActor_CSV_DateRange covers the personal audit
// export (account service's exportAuditLog), which is scoped to a single
// actor across every organization and honors an optional from/to window.
// Unlike ExportByOrganization, the actor column is omitted from the header.
func TestIntegration_ExportByActor_CSV_DateRange(t *testing.T) {
	pool := testPool(t)
	actor := "export_actor_test_" + t.Name()
	orgA, orgB := "export_actor_org_a", "export_actor_org_b"

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE actor = $1`, actor)
	})

	now := time.Now().UTC()
	seedEvent(t, pool, &orgA, actor, "POST", "/organizations/:id/members", now.Add(-48*time.Hour)) // outside window
	seedEvent(t, pool, &orgA, actor, "PATCH", "/organizations/:id", now.Add(-20*time.Hour))        // inside
	seedEvent(t, pool, &orgB, actor, "DELETE", "/organizations/:id/members/:authSub", now)         // inside
	seedEvent(t, pool, &orgA, "someone_else", "POST", "/organizations/:id/members", now)           // wrong actor

	from := now.Add(-36 * time.Hour)
	var buf bytes.Buffer
	if err := audit.ExportByActor(t.Context(), pool, actor, &from, nil, &buf); err != nil {
		t.Fatalf("ExportByActor: %v", err)
	}

	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	wantHeader := []string{"id", "organization_id", "action", "resource", "status_code", "ip", "user_agent", "created_at"}
	if len(rows) == 0 || len(rows[0]) != len(wantHeader) {
		t.Fatalf("header: want %v, got %v", wantHeader, rows[0])
	}
	dataRows := rows[1:]
	if len(dataRows) != 2 {
		t.Fatalf("want 2 rows inside the date window, got %d: %v", len(dataRows), dataRows)
	}
}

// TestIntegration_ListByActorCursor_AcrossOrganizations covers the personal
// "what did I do" trail (account service's listAuditLog) and the GDPR data
// export, both of which read a single actor's events across every
// organization they've acted in, newest first, cursor-paginated by created_at.
func TestIntegration_ListByActorCursor_AcrossOrganizations(t *testing.T) {
	pool := testPool(t)
	actor := "cursor_actor_test_" + t.Name()
	orgA, orgB := "cursor_org_a", "cursor_org_b"

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE actor = $1`, actor)
	})

	base := time.Now().UTC()
	seedEvent(t, pool, &orgA, actor, "POST", "/organizations/:id/members", base)
	seedEvent(t, pool, &orgB, actor, "PATCH", "/organizations/:id", base.Add(-time.Second))
	seedEvent(t, pool, &orgA, actor, "DELETE", "/organizations/:id/members/:authSub", base.Add(-2*time.Second))
	seedEvent(t, pool, &orgA, "not_this_actor", "POST", "/organizations/:id/members", base) // noise

	page1, cursor1, err := audit.ListByActorCursor(t.Context(), pool, actor, 2, "", nil, nil)
	if err != nil {
		t.Fatalf("ListByActorCursor page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1: want 2 events, got %d", len(page1))
	}
	if cursor1 == "" {
		t.Fatalf("page 1: want a next cursor since more events remain")
	}
	for _, e := range page1 {
		if e.Actor != actor {
			t.Errorf("page 1: leaked event from actor %s", e.Actor)
		}
	}

	page2, cursor2, err := audit.ListByActorCursor(t.Context(), pool, actor, 2, cursor1, nil, nil)
	if err != nil {
		t.Fatalf("ListByActorCursor page 2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page 2: want 1 remaining event, got %d", len(page2))
	}
	if cursor2 != "" {
		t.Errorf("page 2: want empty cursor (no more pages), got %q", cursor2)
	}
	if page2[0].OrganizationID == nil || *page2[0].OrganizationID != orgA {
		t.Errorf("page 2: want the oldest event from orgA, got org %v", page2[0].OrganizationID)
	}
}

// TestIntegration_ListByActorCursor_DateRange covers the from/to bound the
// personal audit page's list endpoint now shares with ExportByActor — until
// this was added, GET /me/audit-log ignored from/to entirely (only the CSV
// export honored them), so the visible table's date pickers did nothing.
func TestIntegration_ListByActorCursor_DateRange(t *testing.T) {
	pool := testPool(t)
	actor := "cursor_range_actor_test_" + t.Name()
	orgA := "cursor_range_org_a"

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE actor = $1`, actor)
	})

	now := time.Now().UTC()
	seedEvent(t, pool, &orgA, actor, "POST", "/organizations/:id/members", now.Add(-48*time.Hour)) // outside window
	seedEvent(t, pool, &orgA, actor, "PATCH", "/organizations/:id", now.Add(-20*time.Hour))        // inside
	seedEvent(t, pool, &orgA, actor, "DELETE", "/organizations/:id/members/:authSub", now)         // inside

	from := now.Add(-36 * time.Hour)
	events, cursor, err := audit.ListByActorCursor(t.Context(), pool, actor, 10, "", &from, nil)
	if err != nil {
		t.Fatalf("ListByActorCursor: %v", err)
	}
	if cursor != "" {
		t.Errorf("want empty cursor (no more pages), got %q", cursor)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events inside the date window, got %d", len(events))
	}
	for _, e := range events {
		if e.CreatedAt.Before(from) {
			t.Errorf("event %s created_at %s is before the from bound %s", e.ID, e.CreatedAt, from)
		}
	}
}

// TestIntegration_AnonymizeActor_GDPRDeletion covers the account-deletion
// flow's audit_log step (account service's executeDeleteAccount): the
// actor's identity is scrubbed from every audit event, but the events
// themselves — and every other actor's events — survive.
func TestIntegration_AnonymizeActor_GDPRDeletion(t *testing.T) {
	pool := testPool(t)
	orgID := "anonymize_org_test_" + t.Name()
	deletedSub := "anonymize_actor_test_" + t.Name()
	otherSub := "anonymize_other_test_" + t.Name()

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	now := time.Now().UTC()
	seedEvent(t, pool, &orgID, deletedSub, "POST", "/organizations/:id/members", now)
	seedEvent(t, pool, &orgID, deletedSub, "DELETE", "/organizations/:id/members/:authSub", now.Add(-time.Second))
	seedEvent(t, pool, &orgID, otherSub, "POST", "/organizations/:id/invitations", now.Add(-2*time.Second))

	if err := audit.AnonymizeActor(t.Context(), pool, deletedSub); err != nil {
		t.Fatalf("AnonymizeActor: %v", err)
	}

	var deletedCount, untouchedCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit.events WHERE organization_id = $1 AND actor = 'deleted_user'`,
		orgID).Scan(&deletedCount); err != nil {
		t.Fatalf("count deleted_user: %v", err)
	}
	if deletedCount != 2 {
		t.Errorf("want 2 events anonymized to deleted_user, got %d", deletedCount)
	}

	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit.events WHERE organization_id = $1 AND actor = $2`,
		orgID, otherSub).Scan(&untouchedCount); err != nil {
		t.Fatalf("count other actor: %v", err)
	}
	if untouchedCount != 1 {
		t.Errorf("other actor's event must survive untouched, got %d matching rows", untouchedCount)
	}
}

// TestIntegration_InsertDirect_BypassesWriter covers the write path used by
// call sites outside the /api/v1 middleware chain (e.g. account's Supabase
// webhook handler), which have no gin.Context to hang the batching Writer
// off of and write a single event straight to the table instead.
func TestIntegration_InsertDirect_BypassesWriter(t *testing.T) {
	pool := testPool(t)
	actor := "insert_direct_test_" + t.Name()

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE actor = $1`, actor)
	})

	metadata, _ := json.Marshal(map[string]string{"before": "old@example.com", "after": "new@example.com"})
	if err := audit.InsertDirect(t.Context(), pool, actor, "EMAIL_CHANGE", "/webhooks/supabase/user-updated", 200, metadata); err != nil {
		t.Fatalf("InsertDirect: %v", err)
	}

	events, _, err := audit.ListByActorCursor(t.Context(), pool, actor, 10, "", nil, nil)
	if err != nil {
		t.Fatalf("ListByActorCursor: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event for actor, got %d", len(events))
	}

	e := events[0]
	if e.Action != "EMAIL_CHANGE" {
		t.Errorf("Action = %q, want EMAIL_CHANGE", e.Action)
	}
	if e.Resource != "/webhooks/supabase/user-updated" {
		t.Errorf("Resource = %q, want /webhooks/supabase/user-updated", e.Resource)
	}
	if e.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", e.StatusCode)
	}
	if !bytes.Contains(e.Metadata, []byte("new@example.com")) {
		t.Errorf("Metadata = %s, want it to contain new@example.com", e.Metadata)
	}
}

// TestIntegration_Middleware_BeforeAfter_CapturedInMetadata covers the
// SetBefore/SetAfter diff pattern real handlers use (org settings update,
// subscription plan change): the middleware must merge both into the
// event's metadata alongside the redacted request body.
func TestIntegration_Middleware_BeforeAfter_CapturedInMetadata(t *testing.T) {
	pool := testPool(t)

	writer := audit.NewWriter(pool, slog.Default(), 90)
	defer writer.Stop()

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "before_after_test_sub"})
		c.Next()
	})
	e.Use(writer.Middleware())
	e.PATCH("/audit-before-after-test", func(c *gin.Context) {
		audit.SetBefore(c, map[string]any{"plan": "free"})
		audit.SetAfter(c, map[string]any{"plan": "pro"})
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPatch, "/audit-before-after-test", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	time.Sleep(700 * time.Millisecond) // wait for flush interval

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit.events WHERE resource = '/audit-before-after-test'`)
	})

	var metadata string
	err := pool.QueryRow(t.Context(), `
		SELECT metadata::text FROM audit.events
		WHERE resource = '/audit-before-after-test' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&metadata)
	if err != nil {
		t.Fatalf("audit event not found: %v", err)
	}

	var parsed struct {
		Before map[string]any `json:"before"`
		After  map[string]any `json:"after"`
	}
	if err := json.Unmarshal([]byte(metadata), &parsed); err != nil {
		t.Fatalf("metadata is not valid JSON: %v", err)
	}
	if parsed.Before["plan"] != "free" {
		t.Errorf("before.plan: want free, got %v", parsed.Before["plan"])
	}
	if parsed.After["plan"] != "pro" {
		t.Errorf("after.plan: want pro, got %v", parsed.After["plan"])
	}
}

// TestIntegration_Writer_Cleanup_PurgesEventsOlderThanRetention covers the
// retention-based purge the Writer otherwise only runs from an hourly
// ticker (run()'s cleanupTicker branch) — this drives the same cleanup()
// call synchronously via the export_test.go test hook.
func TestIntegration_Writer_Cleanup_PurgesEventsOlderThanRetention(t *testing.T) {
	pool := testPool(t)
	orgID := "retention_org_test_" + t.Name()

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	now := time.Now().UTC()
	seedEvent(t, pool, &orgID, "retention_actor", "POST", "/organizations/:id/members", now.AddDate(0, 0, -100)) // older than 90-day retention
	seedEvent(t, pool, &orgID, "retention_actor", "PATCH", "/organizations/:id", now.AddDate(0, 0, -10))         // within retention

	writer := audit.NewWriter(pool, slog.Default(), 90)
	defer writer.Stop()
	audit.RunCleanup(writer)

	var remaining int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit.events WHERE organization_id = $1`, orgID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("want 1 event surviving the purge, got %d", remaining)
	}

	var resource string
	if err := pool.QueryRow(t.Context(), `
		SELECT resource FROM audit.events WHERE organization_id = $1`, orgID).Scan(&resource); err != nil {
		t.Fatalf("query survivor: %v", err)
	}
	if resource != "/organizations/:id" {
		t.Errorf("want the within-retention event to survive, got resource %q", resource)
	}
}
