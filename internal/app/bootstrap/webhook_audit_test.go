package bootstrap

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/audit"
)

// A forged provider callback must leave a security record, and it must do so
// through the real route wiring: a mount mistake would otherwise pass every test
// of the middleware alone. The writer runs on the same role production uses.
func TestWebhooks_ForgedCallbackIsAuditedAndAGenuineOneIsNot(t *testing.T) {
	adminDSN, appDSN := os.Getenv("TEST_DATABASE_URL"), os.Getenv("POSTGRES_APP_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("TEST_DATABASE_URL and POSTGRES_APP_URL required")
	}
	admin, err := pgxpool.New(t.Context(), adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(admin.Close)
	app, err := pgxpool.New(t.Context(), appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(app.Close)

	const secret = "whsec_webhook_audit_test"
	const userAgent = "webhook-audit-test-agent"
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE user_agent = $1`, userAgent)
	})

	billingMod := billing.New(admin, billing.ProviderConfig{StripeWebhookSecret: secret}, nil, nil, nil)
	accountMod := account.New(admin, "", "", nil, nil, "", time.Hour)
	writer := audit.NewWriter(app, slog.Default(), 400)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	noLimit := func(c *gin.Context) { c.Next() }
	if err := mountWebhooks(engine, noLimit, writer, billingMod, accountMod); err != nil {
		t.Fatalf("mountWebhooks: %v", err)
	}

	post := func(signature string, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(body))
		req.Header.Set("Stripe-Signature", signature)
		req.Header.Set("User-Agent", userAgent)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Code
	}

	const body = `{"id":"evt_webhook_audit_test","type":"customer.created","data":{"object":{"id":"x"}}}`
	if code := post("t=1,v1=forged", body); code != http.StatusUnauthorized {
		t.Fatalf("forged callback: want 401, got %d", code)
	}

	ts := fmt.Sprint(time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%s", ts, body)
	if code := post("t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)), body); code != http.StatusOK {
		t.Fatalf("genuine callback: want 200, got %d", code)
	}

	// Stop drains the buffer and flushes, so the row is in the table once it returns.
	writer.Stop()

	rows, err := admin.Query(t.Context(), `
		SELECT actor, action, resource, status_code, metadata::text
		FROM audit.events WHERE user_agent = $1`, userAgent)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	var seen int
	for rows.Next() {
		var actor, action, resource, metadata string
		var status int
		if err := rows.Scan(&actor, &action, &resource, &status, &metadata); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if actor != "anonymous" || action != http.MethodPost || resource != "/webhooks/stripe" ||
			status != http.StatusUnauthorized || metadata != "{}" {
			t.Errorf("row = actor=%q action=%q resource=%q status=%d metadata=%s", actor, action, resource, status, metadata)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if seen != 1 {
		t.Errorf("want exactly one audit row (the forged callback), got %d", seen)
	}
}
