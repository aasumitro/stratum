package organization

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpclient"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

// webhookHTTPClient re-validates the resolved IP on every dial, closing the
// SSRF hole a one-time URL check at registration can't (DNS can answer
// differently between registration and delivery). See ValidateOutboundURL
// for the registration-time check.
var webhookHTTPClient = httpclient.SSRFSafeClient()

const (
	// webhookResponseBodyMaxLen caps how much of a receiver's response body
	// is stored for the delivery-detail drawer — full bodies aren't needed
	// for debugging and an unbounded receiver response shouldn't be able to
	// bloat this table.
	webhookResponseBodyMaxLen = 4000
	// webhookHealthWarnThreshold: below this 24h success percentage, the
	// owner gets a one-time (per 24h) warning notification.
	webhookHealthWarnThreshold = 70
)

// WebhookWorker delivers outbound webhook events to customer endpoints, and
// — despite the name, kept as-is to avoid an unrelated rename — also owns
// HandleOrganizationDeleted (see service_organization.go), this module's
// only other worker-side event consumer. Both need the same
// repo/pool/log; store is used only by the latter.
type WebhookWorker struct {
	repo                *repository
	pool                *pgxpool.Pool
	pub                 messaging.EventPublisher
	log                 *slog.Logger
	store               *storage.Client
	secretEncryptionKey string // pgcrypto symmetric key for webhook secret columns, set once at construction
}

// HandleOutboundEvent receives any billing or organization event envelope and fans
// it out to every enabled endpoint subscribed to that event type (per-event
// subscription — empty subscribed_events means every event).
func (w *WebhookWorker) HandleOutboundEvent(ctx context.Context, body []byte) error {
	var env events.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		w.log.Warn("webhook: malformed event envelope", "error", err)
		return nil // don't re-queue bad envelopes
	}
	if env.OrgID == "" {
		return nil
	}

	endpoints, err := w.repo.listEnabledWebhookEndpointsForEvent(ctx, w.pool, env.OrgID, env.Type, w.secretEncryptionKey)
	if err != nil {
		return fmt.Errorf("webhook: list endpoints: %w", err)
	}
	if len(endpoints) == 0 {
		return nil
	}

	for _, ep := range endpoints {
		del, err := w.repo.insertWebhookDelivery(ctx, w.pool, ep.ID, env.ID, env.Type)
		if err != nil {
			w.log.Error("webhook: insert delivery record", "endpoint_id", ep.ID, "error", err)
			continue
		}
		if err := deliver(ctx, w.repo, w.pool, &ep, del, body); err != nil {
			w.log.Warn("webhook: delivery failed", "delivery_id", del.ID, "url", ep.URL, "error", err)
		}
		w.checkHealth(ctx, &ep)
	}
	return nil
}

// HandleWebhookRetry processes one WebhookRetryRequested event — the
// consumer side of the bulk "Retry all failed" action, which dispatches one
// of these per delivery instead of retrying inline on the request goroutine.
func (w *WebhookWorker) HandleWebhookRetry(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.WebhookRetryRequested](body)
	if err != nil {
		w.log.Warn("webhook retry: malformed event", "error", err)
		return nil // don't re-queue a bad envelope
	}

	ep, err := w.repo.findWebhookEndpoint(ctx, w.pool, evt.OrganizationID, evt.EndpointID, w.secretEncryptionKey)
	if err != nil {
		return fmt.Errorf("webhook retry: find endpoint: %w", err)
	}
	del, err := w.repo.findWebhookDelivery(ctx, w.pool, evt.DeliveryID)
	if err != nil {
		return fmt.Errorf("webhook retry: find delivery: %w", err)
	}
	if del.EndpointID != ep.ID {
		w.log.Warn("webhook retry: delivery does not belong to endpoint", "delivery_id", del.ID, "endpoint_id", ep.ID)
		return nil
	}

	payload, err := retryPayload(del, ep.ID)
	if err != nil {
		return fmt.Errorf("webhook retry: payload: %w", err)
	}
	if err := deliver(ctx, w.repo, w.pool, ep, del, payload); err != nil {
		w.log.Warn("webhook retry: delivery failed", "delivery_id", del.ID, "url", ep.URL, "error", err)
	}
	w.checkHealth(ctx, ep)
	return nil
}

// checkHealth runs the health/auto-disable checks after a real delivery
// attempt — lazy, on-write computation, not a scheduled worker, since this
// codebase has no cron-style periodic-sweep infrastructure.
func (w *WebhookWorker) checkHealth(ctx context.Context, ep *webhookEndpointRecord) {
	h, err := w.repo.webhookHealth(ctx, w.pool, ep.ID)
	if err != nil {
		return
	}

	// 3 days of 100% failure -> auto-disable (once; a re-enabled endpoint
	// that fails again gets a fresh 3-day window before this fires again).
	if ep.Enabled && ep.AutoDisabledAt == nil && h.Total3d > 0 && h.Delivered3d == 0 {
		now := time.Now()
		_ = db.WithTx(ctx, w.pool, func(tx db.Querier) error {
			if err := w.repo.setWebhookEnabled(ctx, tx, ep.ID, false, &now); err != nil {
				return err
			}
			return events.Enqueue(ctx, tx, events.ExchangeOrganization,
				events.RoutingKeyWebhookAutoDisabled, "organization", ep.OrganizationID,
				events.WebhookAutoDisabled{OrganizationID: ep.OrganizationID, EndpointID: ep.ID, URL: ep.URL})
		})
		return
	}

	// <70% success in 24h -> one-time-per-24h warning, not a refire on every failure.
	if h.Total24h == 0 {
		return
	}
	percent := int(h.Delivered24h * 100 / h.Total24h)
	if percent >= webhookHealthWarnThreshold {
		return
	}
	if ep.HealthWarnedAt != nil && time.Since(*ep.HealthWarnedAt) < 24*time.Hour {
		return
	}
	_ = db.WithTx(ctx, w.pool, func(tx db.Querier) error {
		if err := w.repo.setWebhookHealthWarnedAt(ctx, tx, ep.ID, time.Now()); err != nil {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization,
			events.RoutingKeyWebhookHealthWarning, "organization", ep.OrganizationID,
			events.WebhookHealthWarning{OrganizationID: ep.OrganizationID, EndpointID: ep.ID, URL: ep.URL, SuccessPercent: percent})
	})
}

// deliver makes one HTTP POST to the endpoint and records the attempt
// (success or failure) as a new entry in the delivery's attempts_log (the
// timeline), plus updates the row's summary columns.
func deliver(
	ctx context.Context, repo *repository, q db.Querier,
	ep *webhookEndpointRecord, del *webhookDeliveryRecord, payload []byte,
) error {
	attemptedAt := time.Now()
	sig := signWebhookPayload(payload, ep.SecretPlaintext, attemptedAt)

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ep.URL, bytes.NewReader(payload))
	if err != nil {
		return recordFailedAttempt(ctx, repo, q, del, attemptedAt, err.Error(), nil, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Stratum-Signature", sig)
	req.Header.Set("X-Stratum-Event", del.EventType)
	// Secret-rotation grace window — sign with the previous secret too, so
	// the receiver can verify against either header while they migrate.
	if ep.SecretPlaintextPrevious != nil && ep.SecretRotationExpiresAt != nil && attemptedAt.Before(*ep.SecretRotationExpiresAt) {
		req.Header.Set("X-Stratum-Signature-Previous", signWebhookPayload(payload, *ep.SecretPlaintextPrevious, attemptedAt))
	}

	start := time.Now()
	resp, err := webhookHTTPClient.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return recordFailedAttempt(ctx, repo, q, del, attemptedAt, err.Error(), nil, &latency)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, webhookResponseBodyMaxLen))
	bodyStr := string(bodyBytes)
	statusCode := resp.StatusCode
	attempts := del.Attempts + 1

	if statusCode >= 200 && statusCode < 300 {
		entry := attemptLogEntry(attempts, &statusCode, &latency, nil, attemptedAt)
		_ = repo.recordWebhookAttempt(ctx, q, del.ID, "delivered",
			attempts, nil, &statusCode, &bodyStr, &latency, &attemptedAt, entry)
		return nil
	}

	errMsg := fmt.Sprintf("HTTP %d", statusCode)
	entry := attemptLogEntry(attempts, &statusCode, &latency, &errMsg, attemptedAt)
	_ = repo.recordWebhookAttempt(ctx, q, del.ID, "failed",
		attempts, &errMsg, &statusCode, &bodyStr, &latency, nil, entry)
	return fmt.Errorf("webhook: endpoint returned %d", statusCode)
}

func recordFailedAttempt(
	ctx context.Context, repo *repository, q db.Querier, del *webhookDeliveryRecord,
	attemptedAt time.Time, errMsg string, statusCode, latency *int,
) error {
	attempts := del.Attempts + 1
	entry := attemptLogEntry(attempts, statusCode, latency, &errMsg, attemptedAt)
	_ = repo.recordWebhookAttempt(ctx, q, del.ID, "failed",
		attempts, &errMsg, statusCode, nil, latency, nil, entry)
	return fmt.Errorf("webhook: %s", errMsg)
}

// attemptLogEntry builds one attempts_log[] element as a JSON array literal
// (e.g. `[{"attempt":1,...}]`) so recordWebhookAttempt can append it
// directly with the jsonb `||` concat operator.
func attemptLogEntry(attempt int, statusCode, latencyMS *int, errMsg *string, at time.Time) json.RawMessage {
	entry := map[string]any{
		"attempt":      attempt,
		"attempted_at": at.Format(time.RFC3339),
	}
	if statusCode != nil {
		entry["status_code"] = *statusCode
	}
	if latencyMS != nil {
		entry["latency_ms"] = *latencyMS
	}
	if errMsg != nil {
		entry["error"] = *errMsg
	}
	b, _ := json.Marshal([]any{entry})
	return b
}

// signWebhookPayload produces a Stripe-style HMAC-SHA256 signature header value.
func signWebhookPayload(payload []byte, secret string, t time.Time) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	return fmt.Sprintf("t=%s,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}
