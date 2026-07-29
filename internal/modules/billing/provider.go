package billing

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aasumitro/stratum/internal/platform/httpclient"
)

// ProviderConfig holds API credentials for Stripe (USD) and Xendit (IDR).
// Leave keys empty to disable the corresponding provider (e.g. in tests).
type ProviderConfig struct {
	StripeAPIKey        string
	StripeWebhookSecret string
	StripeSuccessURL    string
	StripeCancelURL     string
	XenditAPIKey        string
	XenditCallbackToken string
	XenditAllowedCIDRs  []string
}

type paymentLinkResult struct {
	ExternalID string
	URL        string
	ExpiresAt  *time.Time
}

// createStripeCheckoutSession creates a Stripe Checkout Session and returns
// the session ID (used as external_id) and the hosted payment URL.
func createStripeCheckoutSession(
	ctx context.Context, cfg ProviderConfig,
	invoiceID string, amountCents int64, currency string,
) (*paymentLinkResult, error) {
	form := url.Values{}
	form.Set("mode", "payment")
	successURL := cmp.Or(cfg.StripeSuccessURL, "https://app.example.com/billing/success")
	cancelURL := cmp.Or(cfg.StripeCancelURL, "https://app.example.com/billing/cancel")
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	form.Set("line_items[0][price_data][currency]", strings.ToLower(currency))
	form.Set("line_items[0][price_data][product_data][name]", "Invoice "+invoiceID)
	form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", amountCents))
	form.Set("line_items[0][quantity]", "1")
	form.Set("metadata[invoice_id]", invoiceID)
	// Checkout Session metadata is a separate namespace from the PaymentIntent
	// Stripe creates underneath it for mode=payment — it is NOT copied over
	// automatically. payment_intent.payment_failed webhooks carry the
	// PaymentIntent as their data object, so without this, that event type
	// has no way to resolve back to invoiceID at all (see handleStripeWebhook).
	form.Set("payment_intent_data[metadata][invoice_id]", invoiceID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.stripe.com/v1/checkout/sessions",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("billing.createStripeCheckoutSession: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(cfg.StripeAPIKey, "")

	resp, err := httpclient.TrustedClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("billing.createStripeCheckoutSession: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("billing.createStripeCheckoutSession: status %d: %s", resp.StatusCode, body)
	}

	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("billing.createStripeCheckoutSession: decode: %w", err)
	}
	return &paymentLinkResult{ExternalID: out.ID, URL: out.URL}, nil
}

// createXenditInvoice creates a Xendit Invoice and returns its ID and payment URL.
func createXenditInvoice(
	ctx context.Context, cfg ProviderConfig,
	invoiceID string, amountCents int64,
) (*paymentLinkResult, error) {
	payload, _ := json.Marshal(map[string]any{
		"external_id": invoiceID,
		"amount":      amountCents,
		"currency":    currencyIDR,
		"description": "Invoice " + invoiceID,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.xendit.co/v2/invoices",
		strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("billing.createXenditInvoice: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(cfg.XenditAPIKey, "")

	resp, err := httpclient.TrustedClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("billing.createXenditInvoice: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("billing.createXenditInvoice: status %d: %s", resp.StatusCode, body)
	}

	var out struct {
		ID         string     `json:"id"`
		InvoiceURL string     `json:"invoice_url"`
		ExpiryDate *time.Time `json:"expiry_date"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("billing.createXenditInvoice: decode: %w", err)
	}
	return &paymentLinkResult{ExternalID: out.ID, URL: out.InvoiceURL, ExpiresAt: out.ExpiryDate}, nil
}
