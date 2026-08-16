package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// --- webhook routes (public, no auth) ---

// handleStripeWebhook receives Stripe Checkout Session events.
// Signature is verified via HMAC-SHA256 when STRIPE_WEBHOOK_SECRET is set.
//
// @Summary      Stripe webhook
// @Description  Public webhook receiving Stripe Checkout Session and payment_intent events. Verified via HMAC-SHA256 Stripe-Signature header, not bearer auth. Non-relevant event types are ACK'd 200 to stop retries.
// @Tags         webhooks
// @Accept       json
// @Param        Stripe-Signature  header  string  true  "HMAC-SHA256 signature (t=,v1=)"
// @Param        body              body    object  true  "Stripe event payload"
// @Success      200               "ok (processed or ACK'd)"
// @Failure      401               {object}  response.Payload  "invalid signature"
// @Failure      400               {object}  response.Payload  "invalid body or payload"
// @Router       /webhooks/stripe [post]
func (h *handler) handleStripeWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		response.Error(
			"INVALID_BODY",
			"cannot read body",
		).JSON(c, http.StatusBadRequest)
		return
	}

	if !verifyStripeSignature(
		body, c.GetHeader("Stripe-Signature"), h.stripeSecret,
	) {
		response.Error(
			"INVALID_SIGNATURE",
			"invalid signature",
		).JSON(c, http.StatusUnauthorized)
		return
	}

	var payload struct {
		ID   string `json:"id"` // evt_xxx — Stripe event ID for idempotency
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID            string `json:"id"`
				PaymentStatus string `json:"payment_status"` // "paid" | "unpaid" | "no_payment_required"
				Metadata      struct {
					InvoiceID string `json:"invoice_id"`
				} `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		response.Error("INVALID_PAYLOAD", "invalid payload").JSON(c, http.StatusBadRequest)
		return
	}

	// Route by event type — only checkout.session.* and payment_intent.payment_failed are handled.
	// All other event types are ACK'd immediately to prevent Stripe retries.
	var status string
	var invoiceID string
	switch payload.Type {
	case "checkout.session.completed":
		status = normalizeStripeStatus(payload.Data.Object.PaymentStatus)
	case "checkout.session.expired", "payment_intent.payment_failed":
		status = statusFailed
		if payload.Type == "payment_intent.payment_failed" {
			// The data object here is a PaymentIntent (pi_...), not the
			// Checkout Session (cs_...) stored in payment_links.external_id
			// — that lookup would never match, so resolve by invoice_id
			// instead (set on the PaymentIntent at checkout-session
			// creation, see createStripeCheckoutSession).
			invoiceID = payload.Data.Object.Metadata.InvoiceID
		}
	default:
		c.Status(http.StatusOK)
		return
	}

	if payload.Data.Object.ID == "" {
		c.Status(http.StatusOK)
		return
	}

	// DB-level idempotency: only when Stripe provides an event ID (always in production).
	// Skipped when payload.ID is empty so test fixtures without an event ID still work.
	// processWebhook marks-processed and applies all effects in one transaction, so a
	// failure here rolls back the marker too — the provider's retry re-runs it cleanly.
	if err := h.svc.processWebhook(
		c.Request.Context(), "stripe", payload.ID,
		payload.Data.Object.ID, invoiceID, status,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.Status(http.StatusOK) // unknown link — ACK to stop retries
			return
		}
		response.Error(
			"WEBHOOK_FAILED",
			"webhook processing failed",
		).JSON(c, http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

// handleXenditWebhook receives Xendit payment callbacks.
// Token is verified against XENDIT_CALLBACK_TOKEN when set.
//
// @Summary      Xendit webhook
// @Description  Public webhook receiving Xendit payment callbacks. Verified via x-callback-token header, not bearer auth.
// @Tags         webhooks
// @Accept       json
// @Param        x-callback-token  header  string  true  "Shared callback token"
// @Param        body              body    object  true  "Xendit callback payload"
// @Success      200               "ok (processed or ACK'd)"
// @Failure      401               {object}  response.Payload  "invalid token"
// @Failure      400               {object}  response.Payload  "invalid payload"
// @Router       /webhooks/xendit [post]
func (h *handler) handleXenditWebhook(c *gin.Context) {
	if !verifyXenditToken(c.GetHeader("x-callback-token"), h.xenditToken) {
		response.Error(
			"INVALID_TOKEN",
			"invalid token",
		).JSON(c, http.StatusUnauthorized)
		return
	}

	var payload struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.Error(
			"INVALID_PAYLOAD",
			"invalid payload",
		).JSON(c, http.StatusBadRequest)
		return
	}

	if payload.ID == "" {
		c.Status(http.StatusOK)
		return
	}

	status := normalizeXenditStatus(payload.Status)

	// Idempotency key includes the raw status so PAID and EXPIRED deliveries for the
	// same invoice ID are each processed once (terminal states never overlap).
	if err := h.svc.processWebhook(
		c.Request.Context(), "xendit",
		payload.ID+"_"+payload.Status, payload.ID, "", status,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.Status(http.StatusOK)
			return
		}
		response.Error(
			"WEBHOOK_FAILED",
			"webhook processing failed",
		).JSON(c, http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

const stripeSignatureTolerance = 5 * 60 // 5 minutes in seconds

// verifyStripeSignature validates the Stripe-Signature header via HMAC-SHA256.
// An empty secret skips verification — only reachable in development, since
// config.RequireSecretsOutsideDev fails startup otherwise.
func verifyStripeSignature(body []byte, sigHeader, secret string) bool {
	if secret == "" {
		return true
	}
	var ts string
	var sigs []string
	for part := range strings.SplitSeq(sigHeader, ",") {
		if rest, ok := strings.CutPrefix(part, "t="); ok {
			ts = rest
		} else if rest, ok := strings.CutPrefix(part, "v1="); ok {
			sigs = append(sigs, rest)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return false
	}

	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if abs(time.Now().Unix()-tsInt) > stripeSignatureTolerance {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	expected := []byte(hex.EncodeToString(mac.Sum(nil)))
	for _, sig := range sigs {
		if hmac.Equal([]byte(sig), expected) {
			return true
		}
	}
	return false
}

// verifyXenditToken checks the x-callback-token header. An empty
// configToken skips verification — only reachable in development, since
// config.RequireSecretsOutsideDev fails startup otherwise.
func verifyXenditToken(headerToken, configToken string) bool {
	if configToken == "" {
		return true
	}
	return middleware.SecureCompare(headerToken, configToken)
}

func normalizeStripeStatus(s string) string {
	switch s {
	case statusPaid, "no_payment_required":
		return statusPaid
	case "canceled", statusCancelled:
		return statusFailed
	default:
		return statusPending
	}
}

func normalizeXenditStatus(s string) string {
	switch s {
	case "PAID":
		return statusPaid
	case "EXPIRED":
		return statusExpired
	case "FAILED":
		return statusFailed
	default:
		return statusPending
	}
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
