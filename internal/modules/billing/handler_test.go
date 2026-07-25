package billing_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// changePlanRequest.Plan has no format/enum validation at the binding layer
// — a static oneof would reject valid catalog plans added through Studio —
// "unknown plan" is a service-layer, catalog-backed check now
// (TestIntegration_ChangePlan_UnknownPlan). This test covers the binding
// layer's remaining job: cycle is still required/enum-validated.
func TestChangePlan_MissingCycle(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/billing/plan", `{"plan":"enterprise"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestChangePlan_MissingPlan(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/billing/plan", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestChangePlan_MissingTermsAgreed(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/billing/plan",
		`{"plan":"growth","cycle":"monthly"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestChangePlan_TermsAgreedFalse(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/billing/plan",
		`{"plan":"growth","cycle":"monthly","terms_agreed":false}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

// createPaymentLink no longer takes currency from request body — it's resolved from the subscription.
// Tests for invalid/missing currency are no longer applicable.

// --- webhook payload tests (empty secret = skip verification) ---

func TestStripeWebhook_InvalidPayload(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", `not-json`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestXenditWebhook_InvalidPayload(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/xendit", `not-json`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- webhook signature verification tests ---

func TestStripeWebhook_RejectsWhenSecretSetAndNoSignature(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewWebhookEngine("webhook_secret", "").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", `{"type":"checkout.session.completed"}`))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestStripeWebhook_AcceptsValidSignature(t *testing.T) {
	const secret = "webhook_secret"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	body := `{"id":"evt_test","type":"checkout.session.completed","data":{"object":{"id":"cs_test","payment_status":"paid"}}}`

	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%s", ts, body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/stripe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", fmt.Sprintf("t=%s,v1=%s", ts, sig))

	defer func() { recover() }() // svc is nil; panic expected after verification + idempotency check
	w := httptest.NewRecorder()
	billing.NewWebhookEngine(secret, "").ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("valid signature should not get 401")
	}
}

func TestXenditWebhook_RejectsWhenTokenSetAndWrongToken(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/xendit",
		strings.NewReader(`{"id":"inv_test","status":"PAID"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-callback-token", "wrong_token")

	w := httptest.NewRecorder()
	billing.NewWebhookEngine("", "correct_token").ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestXenditWebhook_AcceptsCorrectToken(t *testing.T) {
	const token = "correct_token"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/xendit",
		strings.NewReader(`{"id":"inv_test","status":"PAID"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-callback-token", token)

	defer func() { recover() }() // svc is nil; panic expected after token check passes
	w := httptest.NewRecorder()
	billing.NewWebhookEngine("", token).ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("correct token should not get 401")
	}
}

// --- cancel subscription tests ---

func TestCancelSubscription_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_caller", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/cancel", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestCancelSubscription_OwnerAllowed(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/cancel", `{"reason":"too_expensive"}`))
	if w.Code == http.StatusForbidden {
		t.Errorf("owner should not get 403")
	}
}

func TestCancelSubscription_MissingReason(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/cancel", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestCancelSubscription_InvalidReason(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/cancel", `{"reason":"not_a_real_reason"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestCancelSubscription_DetailsTooLong(t *testing.T) {
	w := httptest.NewRecorder()
	longDetails := strings.Repeat("a", 501)
	billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/cancel",
			`{"reason":"other","details":"`+longDetails+`"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

// Every enum value the frontend can send must bind successfully — a
// mismatch between the frontend's option list and this binding tag would
// otherwise go undetected until a real client sends the value.
func TestCancelSubscription_AllValidReasons(t *testing.T) {
	for _, reason := range []string{
		"too_expensive", "missing_features", "switching_provider",
		"no_longer_needed", "other",
	} {
		t.Run(reason, func(t *testing.T) {
			defer func() { recover() }()
			w := httptest.NewRecorder()
			billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
				httpserver.JSONTestRequest(http.MethodPost, "/billing/cancel", `{"reason":"`+reason+`"}`))
			if w.Code == http.StatusUnprocessableEntity {
				t.Errorf("reason %q should bind successfully, got 422: %s", reason, w.Body)
			}
		})
	}
}

// --- resume subscription tests ---

func TestResumeSubscription_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_caller", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/resume", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestResumeSubscription_OwnerAllowed(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/resume", ""))
	if w.Code == http.StatusForbidden {
		t.Errorf("owner should not get 403")
	}
}

// --- regenerate payment link tests ---

// regeneratePaymentLink no longer takes currency from request body — resolved from subscription.

// --- usage tests ---

func TestRecordUsage_MissingMetric(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/usage", `{"value": 5}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestRecordUsage_ZeroValue(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/usage", `{"metric":"api_calls","value":0}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for zero value, got %d", w.Code)
	}
}

func TestRecordUsage_NegativeValue(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/usage", `{"metric":"api_calls","value":-1}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for negative value, got %d", w.Code)
	}
}

func TestRecordUsage_MissingValueField(t *testing.T) {
	// omitted value defaults to 0 — required rejects zero for int64
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/usage", `{"metric":"api_calls"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing value field, got %d", w.Code)
	}
}

func TestStripeWebhook_EmptyObjectID(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", `{"type":"checkout.session.completed","data":{"object":{"id":"","status":"paid"}}}`))
	if w.Code != http.StatusOK {
		t.Errorf("want 200 ACK for empty id, got %d", w.Code)
	}
}

func TestXenditWebhook_EmptyID(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngine().ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/webhooks/xendit", `{"id":"","status":"PAID"}`))
	if w.Code != http.StatusOK {
		t.Errorf("want 200 ACK for empty id, got %d", w.Code)
	}
}

// --- RBAC tests: GET /billing open to all members, everything else owner-only ---

func TestGetSubscription_MemberAllowed(t *testing.T) {
	defer func() { recover() }() // svc is nil; panic expected after RBAC passes
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_member", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodGet, "/billing", ""))
	if w.Code == http.StatusForbidden {
		t.Errorf("GET /billing must be open to all members, got 403")
	}
}

func TestGetSubscription_OwnerAllowed(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodGet, "/billing", ""))
	if w.Code == http.StatusForbidden {
		t.Errorf("owner should not get 403 on GET /billing")
	}
}

func rbacOwnerOnlyCases() []struct {
	method string
	path   string
	body   string
} {
	return []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/billing/invoices/inv_01/pay", ""},
		{http.MethodPost, "/billing/invoices/inv_01/pay/regenerate", ""},
		{http.MethodPost, "/billing/usage", `{"metric":"members","value":1}`},
		{http.MethodPost, "/billing/extend", `{"months":1}`},
		{http.MethodPost, "/billing/activate", ""},
		{http.MethodPost, "/billing/coupons/redeem", `{"code":"WELCOME10"}`},
		{http.MethodPost, "/billing/addons", `{"addon_id":"extra-seat"}`},
		{http.MethodDelete, "/billing/addons/extra-seat", ""},
	}
}

// rbacMemberVisibleCases: every billing tab's GET is viewable by any member,
// not just the owner. These used to be blanket owner-only, which silently
// broke the read-only member/admin view for everything except the bare
// subscription status check.
func rbacMemberVisibleCases() []struct {
	method string
	path   string
} {
	return []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/billing/history"},
		{http.MethodGet, "/billing/invoices"},
		{http.MethodGet, "/billing/payments"},
		{http.MethodGet, "/billing/payment-links"},
		{http.MethodGet, "/billing/usage"},
		{http.MethodGet, "/billing/features"},
		{http.MethodGet, "/billing/addons"},
		{http.MethodGet, "/billing/preview"},
		{http.MethodGet, "/billing/coupons"},
		{http.MethodGet, "/billing/invoices/inv_01/pdf"},
	}
}

func TestBillingRoutes_MemberCanView(t *testing.T) {
	for _, tc := range rbacMemberVisibleCases() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			defer func() { recover() }() // svc is nil; panic after RBAC passes is expected
			w := httptest.NewRecorder()
			billing.NewHandlerEngineWithCaller("sub_member", "sub_owner").ServeHTTP(w,
				httpserver.JSONTestRequest(tc.method, tc.path, ""))
			if w.Code == http.StatusForbidden {
				t.Errorf("member should not get 403 on %s %s (B2: view=any member)", tc.method, tc.path)
			}
		})
	}
}

func TestBillingRoutes_NonOwnerForbidden(t *testing.T) {
	for _, tc := range rbacOwnerOnlyCases() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			billing.NewHandlerEngineWithCaller("sub_member", "sub_owner").ServeHTTP(w,
				httpserver.JSONTestRequest(tc.method, tc.path, tc.body))
			if w.Code != http.StatusForbidden {
				t.Errorf("want 403 for non-owner on %s %s, got %d", tc.method, tc.path, w.Code)
			}
		})
	}
}

func TestBillingRoutes_AdminForbidden(t *testing.T) {
	for _, tc := range rbacOwnerOnlyCases() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			// admin sub differs from owner — role resolves to member (not owner)
			w := httptest.NewRecorder()
			billing.NewHandlerEngineWithCaller("sub_admin", "sub_owner").ServeHTTP(w,
				httpserver.JSONTestRequest(tc.method, tc.path, tc.body))
			if w.Code != http.StatusForbidden {
				t.Errorf("want 403 for admin on %s %s, got %d", tc.method, tc.path, w.Code)
			}
		})
	}
}

func TestBillingRoutes_OwnerAllowed(t *testing.T) {
	for _, tc := range rbacOwnerOnlyCases() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			defer func() { recover() }() // svc is nil; panic after RBAC passes is expected
			w := httptest.NewRecorder()
			billing.NewHandlerEngineWithCaller("sub_owner", "sub_owner").ServeHTTP(w,
				httpserver.JSONTestRequest(tc.method, tc.path, tc.body))
			if w.Code == http.StatusForbidden {
				t.Errorf("owner should not get 403 on %s %s", tc.method, tc.path)
			}
		})
	}
}
