package organization_test

// Integration tests for webhooks (health, per-event subscriptions,
// secret rotation, test events, auto-disable). Delivery itself needs a real
// HTTP receiver — httptest.Server stands in for the customer endpoint, same
// role a real webhook consumer would play.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

const webhooksTestOrgSlugPrefix = "integ-ws-webhooks-"

func init() {
	// These tests deliver to httptest.Server (loopback, http), which the
	// SSRF guard added for backend-fixes-plan item 1 correctly rejects in
	// production — relax it for this test binary only.
	organization.AllowLoopbackWebhooksForTest()
}

func webhooksURL(orgID string, parts ...string) string {
	url := "/api/organizations/" + orgID + "/webhooks"
	if len(parts) > 0 {
		url += "/" + strings.Join(parts, "/")
	}
	return url
}

// newWebhookFanoutModule gives fan-out tests direct access to
// Module.Worker.HandleOutboundEvent, bypassing RabbitMQ entirely — the
// worker's fan-out logic (per-event subscription filtering) has no HTTP
// surface of its own.
func newWebhookFanoutModule(pool *pgxpool.Pool) *organization.Module {
	return organization.NewModuleForTest(pool)
}

func decodeData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("response has no data object: %s", w.Body.String())
	}
	return data
}

func createWebhookTestOrg(t *testing.T, slugSuffix string) string {
	t.Helper()
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
		`{"slug":"`+webhooksTestOrgSlugPrefix+slugSuffix+`","name":"Webhooks Test Org","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", w.Code, w.Body)
	}
	orgID = decodeData(t, w)["id"].(string)
	return orgID
}

func TestIntegration_Webhooks_CreateListUpdateDelete(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "crud")

	// create with an explicit event subscription
	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID),
		`{"url":"https://example.com/hook","subscribed_events":["organization.created"]}`))
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", wCreate.Code, wCreate.Body)
	}
	created := decodeData(t, wCreate)
	if created["secret"] == nil || created["secret"] == "" {
		t.Error("create: expected a plaintext secret shown once")
	}
	endpointID := created["id"].(string)

	// list — secret must not be present, subscribed_events must round-trip
	wList := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, webhooksURL(orgID), ""))
	if wList.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", wList.Code)
	}
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	endpoints := listResp["data"].([]any)
	if len(endpoints) != 1 {
		t.Fatalf("list: want 1 endpoint, got %d", len(endpoints))
	}
	ep := endpoints[0].(map[string]any)
	if _, hasSecret := ep["secret"]; hasSecret {
		t.Error("list: secret must never be returned outside create/rotate")
	}
	events, _ := ep["subscribed_events"].([]any)
	if len(events) != 1 || events[0] != "organization.created" {
		t.Errorf("list: subscribed_events = %v, want [organization.created]", events)
	}
	if ep["health"] == nil {
		t.Error("list: expected a health object even with zero deliveries")
	}

	// update — url, enabled stays true, widen subscribed_events to all (empty)
	wUpdate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, webhooksURL(orgID, endpointID),
		`{"url":"https://example.com/hook2","enabled":true,"subscribed_events":[]}`))
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d: %s", wUpdate.Code, wUpdate.Body)
	}
	updated := decodeData(t, wUpdate)
	if updated["url"] != "https://example.com/hook2" {
		t.Errorf("update: url = %v, want https://example.com/hook2", updated["url"])
	}

	// delete
	wDelete := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, webhooksURL(orgID, endpointID), ""))
	if wDelete.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", wDelete.Code)
	}
	wListAfter := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, webhooksURL(orgID), ""))
	json.NewDecoder(wListAfter.Body).Decode(&listResp)
	if len(listResp["data"].([]any)) != 0 {
		t.Error("delete: endpoint still present after delete")
	}
}

func TestIntegration_Webhooks_RotateSecret_KeepsPreviousForGraceWindow(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "rotate")

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID), `{"url":"https://example.com/hook"}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)
	originalSecret := created["secret"].(string)

	wRotate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID, endpointID, "rotate-secret"), ""))
	if wRotate.Code != http.StatusOK {
		t.Fatalf("rotate: want 200, got %d: %s", wRotate.Code, wRotate.Body)
	}
	rotated := decodeData(t, wRotate)
	newSecret := rotated["secret"].(string)
	if newSecret == originalSecret {
		t.Error("rotate: new secret must differ from the original")
	}
	if rotated["secret_rotation_expires_at"] == nil {
		t.Error("rotate: expected secret_rotation_expires_at to be set for the grace window")
	}

	var previousSecret string
	var expiresAt time.Time
	err := pool.QueryRow(t.Context(),
		`SELECT secret_plaintext_previous, secret_rotation_expires_at FROM organization.webhook_endpoints WHERE id = $1`,
		endpointID).Scan(&previousSecret, &expiresAt)
	if err != nil {
		t.Fatalf("query rotated row: %v", err)
	}
	if previousSecret != originalSecret {
		t.Errorf("secret_plaintext_previous = %q, want original secret %q", previousSecret, originalSecret)
	}
	if time.Until(expiresAt) < 23*time.Hour || time.Until(expiresAt) > 24*time.Hour {
		t.Errorf("secret_rotation_expires_at not ~24h out: %v", expiresAt)
	}
}

func TestIntegration_Webhooks_SendTestEvent_DeliversAndSigns(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "test-event")

	var receivedSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Stratum-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID), `{"url":"`+srv.URL+`"}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)

	wTest := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID, endpointID, "test-event"), ""))
	if wTest.Code != http.StatusOK {
		t.Fatalf("test-event: want 200, got %d: %s", wTest.Code, wTest.Body)
	}
	testResp := decodeData(t, wTest)
	if testResp["success"] != true {
		t.Errorf("test-event: success = %v, want true", testResp["success"])
	}
	if receivedSig == "" {
		t.Error("test-event: receiver never got a X-Stratum-Signature header")
	}

	var status string
	err := pool.QueryRow(t.Context(),
		`SELECT status FROM organization.webhook_deliveries WHERE endpoint_id = $1 ORDER BY created_at DESC LIMIT 1`,
		endpointID).Scan(&status)
	if err != nil {
		t.Fatalf("query delivery row: %v", err)
	}
	if status != "delivered" {
		t.Errorf("delivery status = %q, want delivered", status)
	}
}

func TestIntegration_Webhooks_UpdateEnabled_BlockedWhileAutoDisabled(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "guard")

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID), `{"url":"https://example.com/hook"}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)

	if _, err := pool.Exec(t.Context(),
		`UPDATE organization.webhook_endpoints SET enabled = false, auto_disabled_at = now() WHERE id = $1`, endpointID); err != nil {
		t.Fatalf("seed auto_disabled_at: %v", err)
	}

	wUpdate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, webhooksURL(orgID, endpointID),
		`{"url":"https://example.com/hook","enabled":true}`))
	if wUpdate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update while auto-disabled: want 422, got %d: %s", wUpdate.Code, wUpdate.Body)
	}
}

func TestIntegration_Webhooks_SendTestEvent_ReenablesAutoDisabled(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "reenable")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID), `{"url":"`+srv.URL+`"}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)

	if _, err := pool.Exec(t.Context(),
		`UPDATE organization.webhook_endpoints SET enabled = false, auto_disabled_at = now() WHERE id = $1`, endpointID); err != nil {
		t.Fatalf("seed auto_disabled_at: %v", err)
	}

	wTest := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID, endpointID, "test-event"), ""))
	if wTest.Code != http.StatusOK {
		t.Fatalf("test-event: want 200, got %d: %s", wTest.Code, wTest.Body)
	}

	var enabled bool
	var autoDisabledAt *time.Time
	err := pool.QueryRow(t.Context(),
		`SELECT enabled, auto_disabled_at FROM organization.webhook_endpoints WHERE id = $1`, endpointID).Scan(&enabled, &autoDisabledAt)
	if err != nil {
		t.Fatalf("query row: %v", err)
	}
	if !enabled {
		t.Error("a passing test event on an auto-disabled endpoint should re-enable it")
	}
	if autoDisabledAt != nil {
		t.Error("auto_disabled_at should be cleared after a passing test event")
	}
}

func TestIntegration_Webhooks_ListDeliveries_FiltersByStatusAndEventType(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "deliveries-filter")

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID), `{"url":"https://example.com/hook"}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)

	seed := []struct{ status, eventType string }{
		{"delivered", "organization.created"},
		{"failed", "organization.created"},
		{"failed", "member.invited"},
	}
	for _, s := range seed {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type, status) VALUES ($1, $2, $3, $4)`,
			endpointID, "evt-"+s.status+"-"+s.eventType, s.eventType, s.status); err != nil {
			t.Fatalf("seed delivery: %v", err)
		}
	}

	wList := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, webhooksURL(orgID, endpointID, "deliveries")+"?status=failed", ""))
	if wList.Code != http.StatusOK {
		t.Fatalf("list deliveries: want 200, got %d: %s", wList.Code, wList.Body)
	}
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	if got := len(listResp["data"].([]any)); got != 2 {
		t.Errorf("status=failed: want 2 deliveries, got %d", got)
	}

	wList2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet,
		webhooksURL(orgID, endpointID, "deliveries")+"?status=failed&event_type=member.invited", ""))
	json.NewDecoder(wList2.Body).Decode(&listResp)
	if got := len(listResp["data"].([]any)); got != 1 {
		t.Errorf("status=failed&event_type=member.invited: want 1 delivery, got %d", got)
	}
}

func TestIntegration_Webhooks_ListDeliveries_CursorPagination(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "deliveries-cursor")

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID), `{"url":"https://example.com/hook"}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)

	base := time.Now().Add(-time.Hour)
	for i := range 5 {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type, status, created_at)
			 VALUES ($1, $2, 'organization.created', 'delivered', $3)`,
			endpointID, fmt.Sprintf("evt-%d", i), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed delivery %d: %v", i, err)
		}
	}

	w1 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, webhooksURL(orgID, endpointID, "deliveries")+"?limit=3", ""))
	if w1.Code != http.StatusOK {
		t.Fatalf("page 1: want 200, got %d: %s", w1.Code, w1.Body)
	}
	var resp1 map[string]any
	json.NewDecoder(w1.Body).Decode(&resp1)
	page1, _ := resp1["data"].([]any)
	if len(page1) != 3 {
		t.Fatalf("page 1: want 3 deliveries, got %d", len(page1))
	}
	nextCursor, _ := resp1["next_cursor"].(string)
	if nextCursor == "" {
		t.Fatal("page 1: want a non-empty next_cursor (5 seeded, limit 3)")
	}
	if _, ok := resp1["pagination"]; ok {
		t.Error("cursor mode: want no pagination object in the response")
	}

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet,
		webhooksURL(orgID, endpointID, "deliveries")+"?limit=3&cursor="+url.QueryEscape(nextCursor), ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("page 2: want 200, got %d: %s", w2.Code, w2.Body)
	}
	var resp2 map[string]any
	json.NewDecoder(w2.Body).Decode(&resp2)
	page2, _ := resp2["data"].([]any)
	if len(page2) != 2 {
		t.Fatalf("page 2: want 2 remaining deliveries, got %d", len(page2))
	}
	if nc2, _ := resp2["next_cursor"].(string); nc2 != "" {
		t.Errorf("page 2: want empty next_cursor (exhausted), got %q", nc2)
	}

	seen := map[string]bool{}
	for _, row := range append(append([]any{}, page1...), page2...) {
		id, _ := row.(map[string]any)["id"].(string)
		if seen[id] {
			t.Errorf("delivery %s appeared on both pages", id)
		}
		seen[id] = true
	}
	if len(seen) != 5 {
		t.Errorf("want 5 distinct deliveries across both pages, got %d", len(seen))
	}
}

// capturingPublisher records every published event instead of sending it
// anywhere — lets a test simulate what the real RabbitMQ consumer would do
// with a published event, without needing a broker in this test binary.
type capturingPublisher struct {
	published []capturedEvent
}

type capturedEvent struct {
	exchange, routingKey string
	body                 []byte
}

func (p *capturingPublisher) Publish(_ context.Context, exchange, routingKey string, body []byte) error {
	p.published = append(p.published, capturedEvent{exchange, routingKey, body})
	return nil
}

func (p *capturingPublisher) PublishDelayed(_ context.Context, exchange, routingKey string, body []byte, _ time.Duration) error {
	return p.Publish(context.Background(), exchange, routingKey, body)
}

// TestIntegration_Webhooks_RetryAllFailed regression-tests that the bulk
// "Retry all failed" action queues one WebhookRetryRequested event per
// delivery (via a captured publisher, since there's no broker in this test
// binary) instead of retrying inline on the request goroutine, and that
// feeding those events through the worker's consumer handler produces the
// same end result — every failed delivery actually retried.
func TestIntegration_Webhooks_RetryAllFailed(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "retry-all")
	pub := &capturingPublisher{}
	e, mod := organization.NewModuleEngineWithPublisher(pool, testAuthSub, pub)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wCreate := httptest.NewRecorder()
	e.ServeHTTP(wCreate, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID), `{"url":"`+srv.URL+`"}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)

	for i := range 2 {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type, status) VALUES ($1, $2, 'organization.created', 'failed')`,
			endpointID, "evt-retry-"+string(rune('a'+i))); err != nil {
			t.Fatalf("seed failed delivery: %v", err)
		}
	}

	wRetry := httptest.NewRecorder()
	e.ServeHTTP(wRetry, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID, endpointID, "deliveries", "retry-failed"), ""))
	if wRetry.Code != http.StatusOK {
		t.Fatalf("retry-failed: want 200, got %d: %s", wRetry.Code, wRetry.Body)
	}
	retried := decodeData(t, wRetry)
	if int(retried["retried"].(float64)) != 2 {
		t.Errorf("retried = %v, want 2", retried["retried"])
	}

	// nothing should have actually been delivered yet — the request
	// goroutine only queues the retry jobs.
	if hits != 0 {
		t.Fatalf("receiver hits after retry-failed call = %d, want 0 (delivery happens on the worker consumer)", hits)
	}
	if len(pub.published) != 2 {
		t.Fatalf("published events = %d, want 2", len(pub.published))
	}

	// simulate the worker consumer processing each queued retry job.
	for _, evt := range pub.published {
		if evt.routingKey != "organization.webhook.retry-requested" {
			t.Fatalf("unexpected routing key %q", evt.routingKey)
		}
		if err := mod.Worker.HandleWebhookRetry(t.Context(), evt.body); err != nil {
			t.Fatalf("HandleWebhookRetry: %v", err)
		}
	}
	if hits != 2 {
		t.Errorf("receiver hits after processing retry jobs = %d, want 2", hits)
	}
}

func TestIntegration_Webhooks_SubscribedEvents_FiltersFanOut(t *testing.T) {
	pool := testPool(t)
	orgID := createWebhookTestOrg(t, "fanout")
	mod := newWebhookFanoutModule(pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// subscribed only to "billing.invoice.created" — should NOT receive "organization.created"
	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, webhooksURL(orgID),
		`{"url":"`+srv.URL+`","subscribed_events":["billing.invoice.created"]}`))
	created := decodeData(t, wCreate)
	endpointID := created["id"].(string)

	envelope := []byte(`{"id":"evt-1","type":"organization.created","org_id":"` + orgID + `"}`)
	if err := mod.Worker.HandleOutboundEvent(t.Context(), envelope); err != nil {
		t.Fatalf("HandleOutboundEvent: %v", err)
	}

	var count int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM organization.webhook_deliveries WHERE endpoint_id = $1`, endpointID).Scan(&count); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if count != 0 {
		t.Errorf("endpoint not subscribed to organization.created received %d deliveries, want 0", count)
	}

	envelope2 := []byte(`{"id":"evt-2","type":"billing.invoice.created","org_id":"` + orgID + `"}`)
	if err := mod.Worker.HandleOutboundEvent(t.Context(), envelope2); err != nil {
		t.Fatalf("HandleOutboundEvent: %v", err)
	}
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM organization.webhook_deliveries WHERE endpoint_id = $1`, endpointID).Scan(&count); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if count != 1 {
		t.Errorf("endpoint subscribed to billing.invoice.created received %d deliveries, want 1", count)
	}
}
