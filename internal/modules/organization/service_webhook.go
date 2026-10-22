package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpclient"
	"github.com/aasumitro/stratum/internal/platform/logger"
)

// ── Webhook endpoints ──────────────────────────────────────────────────────

// webhookFeatureID is the billing.features slug gating webhook creation to
// Growth+ plans — already seeded in 000004_billing.up.sql, this is the
// first call site wiring it up.
const webhookFeatureID = "webhooks"

// Shared JSON keys for the synthetic payloads webhook retries/test-events
// re-send — a real published event envelope isn't retained, so these are
// reconstructed from the stored delivery row instead.
const (
	eventPayloadIDKey         = "id"
	eventPayloadTypeKey       = "type"
	eventPayloadEndpointIDKey = "endpoint_id"
)

// ErrWebhookRotationTestRequired guards the re-enable rule: an auto-disabled
// endpoint can't be flipped back on directly, only via a passing test event
// (enableWebhookAfterTest).
var ErrWebhookRotationTestRequired = errors.New("organization: auto-disabled endpoint requires a passing test event to re-enable")

// ErrWebhookFeatureNotAvailable - webhooks are a Growth+ catalog feature
// (billing.features id "webhooks", already seeded for growth/custom plans);
// a Solo-plan organization sees a feature-gate card instead.
var ErrWebhookFeatureNotAvailable = errors.New("organization: webhooks are not available on the current plan")

// ErrWebhookURLNotAllowed - the URL failed the SSRF trust-boundary check
// (must be https, must resolve to a publicly routable address).
var ErrWebhookURLNotAllowed = errors.New("organization: webhook url is not allowed")

// ErrWebhookDeliveryMismatch - the delivery ID exists but belongs to a
// different endpoint than the one in the request path. Maps to the same
// 404 as a delivery ID that doesn't exist at all, deliberately — telling
// the two apart would confirm to a caller that a delivery ID they don't
// own exists somewhere, which the plain "not found" response never does.
var ErrWebhookDeliveryMismatch = errors.New("organization: webhook delivery does not belong to this endpoint")

// validateWebhookURL is a var so tests can relax it for httptest.Server
// (loopback, http) stand-in receivers — see AllowLoopbackWebhooksForTest in
// export_test.go.
var validateWebhookURL = httpclient.ValidateOutboundURL

func (s *service) createWebhookEndpoint(
	ctx context.Context, organizationID, url string, subscribedEvents []string,
) (rec *webhookEndpointRecord, secret string, err error) {
	defer func() {
		if err == nil {
			return
		}
		rec, secret = nil, ""
		switch {
		case errors.Is(err, ErrWebhookFeatureNotAvailable):
			err = apperr.Forbidden("FEATURE_NOT_AVAILABLE", "webhooks are not available on the current plan")
		case errors.Is(err, ErrWebhookURLNotAllowed):
			err = apperr.Validation("WEBHOOK_URL_NOT_ALLOWED", "webhook url must be https and resolve to a public address")
		default:
			logger.FromContext(ctx).Error("createWebhook failed", "error", err)
			err = apperr.Internal("WEBHOOK_CREATE_FAILED", "failed to create webhook", err)
		}
	}()

	if s.billingReader != nil {
		if err := s.billingReader.CheckFeatureAccess(ctx, organizationID, webhookFeatureID); err != nil {
			return nil, "", ErrWebhookFeatureNotAvailable
		}
	}
	if err := validateWebhookURL(ctx, url); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrWebhookURLNotAllowed, err)
	}
	secret, err = generateToken(64)
	if err != nil {
		return nil, "", fmt.Errorf("organization.createWebhookEndpoint: %w", err)
	}
	err = db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		var err error
		rec, err = s.repo.insertWebhookEndpoint(ctx, q, organizationID, url, secret, subscribedEvents, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
		return err
	})
	if err != nil {
		return nil, "", fmt.Errorf("organization.createWebhookEndpoint: %w", err)
	}
	return rec, secret, nil
}

func (s *service) listWebhookEndpoints(ctx context.Context, organizationID string) ([]webhookEndpointRecord, error) {
	var recs []webhookEndpointRecord
	err := db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		var err error
		recs, err = s.repo.listWebhookEndpoints(ctx, q, organizationID, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
		return err
	})
	if err != nil {
		return nil, apperr.Internal("WEBHOOK_LIST_FAILED", "failed to list webhooks", err)
	}
	return recs, nil
}

func (s *service) updateWebhookEndpoint(
	ctx context.Context, organizationID, id, url string,
	enabled bool, subscribedEvents []string,
) (rec *webhookEndpointRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		rec = nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("WEBHOOK_NOT_FOUND", "webhook not found", err)
		case errors.Is(err, ErrWebhookURLNotAllowed):
			err = apperr.Validation("WEBHOOK_URL_NOT_ALLOWED", "webhook url must be https and resolve to a public address")
		case errors.Is(err, ErrWebhookRotationTestRequired):
			err = apperr.Validation("WEBHOOK_REQUIRES_TEST_EVENT", "send a passing test event to re-enable this webhook")
		default:
			err = apperr.Internal("WEBHOOK_UPDATE_FAILED", "failed to update webhook", err)
		}
	}()

	if err := validateWebhookURL(ctx, url); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWebhookURLNotAllowed, err)
	}
	err = db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		if enabled {
			ep, err := s.repo.findWebhookEndpoint(ctx, q, organizationID, id, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
			if err != nil {
				return fmt.Errorf("organization.updateWebhookEndpoint: %w", err)
			}
			// An auto-disabled endpoint can only be re-enabled by a passing
			// test event (enableWebhookAfterTest), not a plain toggle.
			if ep.AutoDisabledAt != nil {
				return ErrWebhookRotationTestRequired
			}
		}
		var err error
		rec, err = s.repo.updateWebhookEndpoint(ctx, q, organizationID, id, url, enabled, subscribedEvents, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
		return err
	})
	return rec, err
}

// rotateWebhookSecret issues a fresh secret, keeping the old one valid for a
// 24h grace window so the receiver can migrate without downtime.
func (s *service) rotateWebhookSecret(
	ctx context.Context, organizationID, id string,
) (rec *webhookEndpointRecord, newSecret string, err error) {
	defer func() {
		if err == nil {
			return
		}
		rec, newSecret = nil, ""
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperr.NotFound("WEBHOOK_NOT_FOUND", "webhook not found", err)
			return
		}
		err = apperr.Internal("WEBHOOK_ROTATE_FAILED", "failed to rotate secret", err)
	}()

	newSecret, err = generateToken(64)
	if err != nil {
		return nil, "", fmt.Errorf("organization.rotateWebhookSecret: %w", err)
	}
	err = db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		var err error
		rec, err = s.repo.rotateWebhookSecret(ctx, q, organizationID, id, newSecret, time.Now().Add(24*time.Hour), s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
		return err
	})
	if err != nil {
		return nil, "", fmt.Errorf("organization.rotateWebhookSecret: %w", err)
	}
	return rec, newSecret, nil
}

func (s *service) deleteWebhookEndpoint(ctx context.Context, organizationID, id string) error {
	var deleted bool
	err := db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		var err error
		deleted, err = s.repo.deleteWebhookEndpoint(ctx, q, organizationID, id)
		return err
	})
	if err != nil {
		return apperr.Internal("WEBHOOK_DELETE_FAILED", "failed to delete webhook", err)
	}
	if !deleted {
		return apperr.NotFound("WEBHOOK_NOT_FOUND", "webhook not found", nil)
	}
	return nil
}

func (s *service) getWebhookHealthBatch(ctx context.Context, organizationID string, endpointIDs []string) (out map[string]webhookHealth, err error) {
	err = db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		out, err = s.repo.webhookHealthBatch(ctx, q, endpointIDs)
		return err
	})
	return out, err
}

// findWebhookEndpointForOrganization reports whether webhookID exists in
// organizationID (pgx.ErrNoRows otherwise) — the ownership check the
// delivery-list handler runs before listing.
func (s *service) findWebhookEndpointForOrganization(ctx context.Context, organizationID, webhookID string) error {
	return db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		_, err := s.repo.findWebhookEndpoint(ctx, q, organizationID, webhookID, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
		return err
	})
}

func (s *service) listWebhookDeliveries(
	ctx context.Context, organizationID, endpointID string,
	filter webhookDeliveryFilter, cursor string, limit int,
) (recs []webhookDeliveryRecord, next string, err error) {
	err = db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		recs, next, err = s.repo.listWebhookDeliveries(ctx, q, endpointID, filter, cursor, limit)
		return err
	})
	return recs, next, err
}

func (s *service) retryWebhookDelivery(ctx context.Context, organizationID, webhookID, deliveryID string) (err error) {
	defer func() {
		if err != nil {
			logger.FromContext(ctx).Error("retryWebhookDelivery failed", "error", err)
			switch {
			case errors.Is(err, pgx.ErrNoRows), errors.Is(err, ErrWebhookDeliveryMismatch):
				err = apperr.NotFound("WEBHOOK_NOT_FOUND", "webhook not found", err)
			default:
				err = apperr.Internal("RETRY_FAILED", "retry failed", err)
			}
		}
	}()

	// deliver makes an outbound HTTP call between single statements, so no
	// transaction may span it: this path reads and writes through the
	// unscoped pool, matched by the organization + endpoint + delivery
	// predicates below (the route already passed the organization middleware).
	pool := s.unscoped()
	ep, err := s.repo.findWebhookEndpoint(ctx, pool, organizationID, webhookID, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
	if err != nil {
		return fmt.Errorf("organization.retryWebhookDelivery: endpoint: %w", err)
	}
	del, err := s.repo.findWebhookDelivery(ctx, pool, deliveryID)
	if err != nil {
		return fmt.Errorf("organization.retryWebhookDelivery: delivery: %w", err)
	}
	if del.EndpointID != ep.ID {
		return fmt.Errorf("organization.retryWebhookDelivery: %w", ErrWebhookDeliveryMismatch)
	}
	payload, err := retryPayload(del, ep.ID)
	if err != nil {
		return fmt.Errorf("organization.retryWebhookDelivery: payload: %w", err)
	}
	return deliver(ctx, s.repo, pool, ep, del, payload)
}

// retryAllFailedWebhookDeliveries backs the bulk "Retry all failed" action.
// Each delivery is dispatched as its own WebhookRetryRequested event rather
// than retried inline: looping deliver() synchronously here would run up to
// 50 outbound HTTP calls (up to 10s timeout each) on the request goroutine,
// tying it up for minutes and giving one bulk click an outsized amplification
// lever. The worker's consumer (rate-limited by its PrefetchCount) does the
// actual attempts instead. Returns how many were queued, not how many
// ultimately succeed.
func (s *service) retryAllFailedWebhookDeliveries(
	ctx context.Context, organizationID, webhookID string,
) (n int, err error) {
	defer func() {
		if err != nil {
			logger.FromContext(ctx).Error("retryAllFailedWebhookDeliveries failed", "error", err)
			n = 0
			if errors.Is(err, pgx.ErrNoRows) {
				err = apperr.NotFound("WEBHOOK_NOT_FOUND", "webhook not found", err)
				return
			}
			err = apperr.Internal("RETRY_FAILED", "retry failed", err)
		}
	}()

	var dels []webhookDeliveryRecord
	err = db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		if _, err := s.repo.findWebhookEndpoint(ctx, q, organizationID, webhookID, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious); err != nil {
			return fmt.Errorf("organization.retryAllFailedWebhookDeliveries: endpoint: %w", err)
		}
		var err error
		dels, err = s.repo.listFailedWebhookDeliveries(ctx, q, webhookID)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("organization.retryAllFailedWebhookDeliveries: %w", err)
	}
	for i := range dels {
		if err := events.Enqueue(ctx, s.pool, events.ExchangeOrganization, events.RoutingKeyWebhookRetryRequested, "organization", organizationID,
			events.WebhookRetryRequested{OrganizationID: organizationID, EndpointID: webhookID, DeliveryID: dels[i].ID}); err != nil {
			return 0, fmt.Errorf("organization.retryAllFailedWebhookDeliveries: enqueue: %w", err)
		}
	}
	return len(dels), nil
}

// retryPayload re-sends a minimal envelope built from the stored event
// metadata — the original event payload isn't retained, a known limitation
// carried forward unchanged from before this milestone.
func retryPayload(del *webhookDeliveryRecord, endpointID string) ([]byte, error) {
	payload, err := json.Marshal(map[string]string{
		eventPayloadIDKey:         del.EventID,
		eventPayloadTypeKey:       del.EventType,
		eventPayloadEndpointIDKey: endpointID,
	})
	if err != nil {
		return nil, fmt.Errorf("organization.retryPayload: %w", err)
	}
	return payload, nil
}

// sendTestEvent delivers a synthetic payload through the exact same
// signing/recording path as a real event. A passing (2xx) test on an
// auto-disabled endpoint also clears the auto-disable — the one sanctioned
// re-enable path.
func (s *service) sendTestEvent(
	ctx context.Context, organizationID, webhookID string,
) (del *webhookDeliveryRecord, success bool, err error) {
	defer func() {
		if err == nil {
			return
		}
		del, success = nil, false
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperr.NotFound("WEBHOOK_NOT_FOUND", "webhook not found", err)
			return
		}
		err = apperr.Internal("WEBHOOK_TEST_FAILED", "failed to send test event", err)
	}()

	// Unscoped pool for the same reason as retryWebhookDelivery: deliver's
	// outbound HTTP call sits between these single statements.
	pool := s.unscoped()
	ep, err := s.repo.findWebhookEndpoint(ctx, pool, organizationID, webhookID, s.secretEncryptionKeyVersion, s.secretEncryptionKey, s.secretEncryptionKeyPrevious)
	if err != nil {
		return nil, false, fmt.Errorf("organization.sendTestEvent: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		eventPayloadIDKey:   "test-" + time.Now().Format("20060102150405"),
		eventPayloadTypeKey: "test.ping",
		"org":               organizationID,
		"message":           "This is a test event from Stratum.",
	})
	del, err = s.repo.insertWebhookDelivery(ctx, pool, ep.ID, "test-event", "test.ping")
	if err != nil {
		return nil, false, fmt.Errorf("organization.sendTestEvent: %w", err)
	}
	success = deliver(ctx, s.repo, pool, ep, del, payload) == nil
	updated, err := s.repo.findWebhookDelivery(ctx, pool, del.ID)
	if err != nil {
		updated = del
	}
	if success && ep.AutoDisabledAt != nil {
		if err := s.repo.setWebhookEnabled(ctx, pool, ep.ID, true, nil); err != nil {
			return updated, success, fmt.Errorf("organization.sendTestEvent: re-enable: %w", err)
		}
	}
	return updated, success, nil
}
