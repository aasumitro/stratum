package organization

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

// ── Webhook endpoints ──────────────────────────────────────────────────────

type webhookEndpointRecord struct {
	ID                      string     `json:"id"`
	OrganizationID          string     `json:"organization_id"`
	URL                     string     `json:"url"`
	SecretPlaintext         string     `json:"secret_plaintext"`
	SecretPlaintextPrevious *string    `json:"-"`
	SecretRotationExpiresAt *time.Time `json:"secret_rotation_expires_at,omitempty"`
	SubscribedEvents        []string   `json:"subscribed_events,omitempty"`
	Enabled                 bool       `json:"enabled"`
	AutoDisabledAt          *time.Time `json:"auto_disabled_at,omitempty"`
	HealthWarnedAt          *time.Time `json:"-"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

const webhookEndpointColumns = `id, organization_id, url, secret_plaintext, secret_plaintext_previous,
	secret_rotation_expires_at, subscribed_events, enabled, auto_disabled_at, health_warned_at,
	created_at, updated_at`

func scanWebhookEndpoint(row interface{ Scan(dest ...any) error }, rec *webhookEndpointRecord) error {
	return row.Scan(
		&rec.ID, &rec.OrganizationID, &rec.URL, &rec.SecretPlaintext, &rec.SecretPlaintextPrevious,
		&rec.SecretRotationExpiresAt, &rec.SubscribedEvents, &rec.Enabled, &rec.AutoDisabledAt, &rec.HealthWarnedAt,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
}

func (r *repository) insertWebhookEndpoint(
	ctx context.Context, q db.Querier,
	organizationID, url, secretPlaintext string, subscribedEvents []string,
) (*webhookEndpointRecord, error) {
	rec := new(webhookEndpointRecord)
	err := scanWebhookEndpoint(q.QueryRow(ctx, `
		INSERT INTO organization.webhook_endpoints (organization_id, url, secret_plaintext, subscribed_events)
		VALUES ($1, $2, $3, $4)
		RETURNING `+webhookEndpointColumns,
		organizationID, url, secretPlaintext, subscribedEvents,
	), rec)
	if err != nil {
		return nil, fmt.Errorf("organization.insertWebhookEndpoint: %w", err)
	}
	return rec, nil
}

func (r *repository) listWebhookEndpoints(
	ctx context.Context, q db.Querier,
	organizationID string,
) ([]webhookEndpointRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT `+webhookEndpointColumns+`
		FROM organization.webhook_endpoints
		WHERE organization_id = $1
		ORDER BY created_at DESC`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.listWebhookEndpoints: %w", err)
	}
	defer rows.Close()

	recs := []webhookEndpointRecord{}
	for rows.Next() {
		var rec webhookEndpointRecord
		if err := scanWebhookEndpoint(rows, &rec); err != nil {
			return nil, fmt.Errorf("organization.listWebhookEndpoints: scan: %w", err)
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listWebhookEndpoints: %w", err)
	}
	return recs, nil
}

// listEnabledWebhookEndpointsForEvent returns every enabled endpoint that
// should receive eventType — either subscribed_events is empty/NULL (all
// events, same "empty = allow all" convention as organization settings'
// allowed_ips) or eventType is explicitly in the list.
func (r *repository) listEnabledWebhookEndpointsForEvent(
	ctx context.Context, q db.Querier,
	organizationID, eventType string,
) ([]webhookEndpointRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT `+webhookEndpointColumns+`
		FROM organization.webhook_endpoints
		WHERE organization_id = $1 AND enabled = true
		  AND (subscribed_events IS NULL OR array_length(subscribed_events, 1) IS NULL OR $2 = ANY(subscribed_events))
		ORDER BY created_at`,
		organizationID, eventType,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.listEnabledWebhookEndpointsForEvent: %w", err)
	}
	defer rows.Close()

	recs := []webhookEndpointRecord{}
	for rows.Next() {
		var rec webhookEndpointRecord
		if err := scanWebhookEndpoint(rows, &rec); err != nil {
			return nil, fmt.Errorf("organization.listEnabledWebhookEndpointsForEvent: scan: %w", err)
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listEnabledWebhookEndpointsForEvent: %w", err)
	}
	return recs, nil
}

func (r *repository) findWebhookEndpoint(
	ctx context.Context, q db.Querier,
	organizationID, id string,
) (*webhookEndpointRecord, error) {
	rec := new(webhookEndpointRecord)
	err := scanWebhookEndpoint(q.QueryRow(ctx, `
		SELECT `+webhookEndpointColumns+`
		FROM organization.webhook_endpoints
		WHERE organization_id = $1 AND id = $2`,
		organizationID, id,
	), rec)
	if err != nil {
		return nil, fmt.Errorf("organization.findWebhookEndpoint: %w", err)
	}
	return rec, nil
}

func (r *repository) updateWebhookEndpoint(
	ctx context.Context, q db.Querier,
	organizationID, id, url string, enabled bool, subscribedEvents []string,
) (*webhookEndpointRecord, error) {
	rec := new(webhookEndpointRecord)
	err := scanWebhookEndpoint(q.QueryRow(ctx, `
		UPDATE organization.webhook_endpoints
		SET url = $3, enabled = $4, subscribed_events = $5, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING `+webhookEndpointColumns,
		organizationID, id, url, enabled, subscribedEvents,
	), rec)
	if err != nil {
		return nil, fmt.Errorf("organization.updateWebhookEndpoint: %w", err)
	}
	return rec, nil
}

// rotateWebhookSecret sets a fresh secret as current, keeps the old one as
// secret_plaintext_previous for the 24h grace window deliver() signs with,
// and returns the new plaintext secret (shown once, same convention as create).
func (r *repository) rotateWebhookSecret(
	ctx context.Context, q db.Querier,
	organizationID, id, newSecret string, graceExpiresAt time.Time,
) (*webhookEndpointRecord, error) {
	rec := new(webhookEndpointRecord)
	err := scanWebhookEndpoint(q.QueryRow(ctx, `
		UPDATE organization.webhook_endpoints
		SET secret_plaintext_previous = secret_plaintext, secret_plaintext = $3, secret_rotation_expires_at = $4, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING `+webhookEndpointColumns,
		organizationID, id, newSecret, graceExpiresAt,
	), rec)
	if err != nil {
		return nil, fmt.Errorf("organization.rotateWebhookSecret: %w", err)
	}
	return rec, nil
}

// setWebhookEnabled is a narrower update than updateWebhookEndpoint — used
// by the test-event re-enable flow (clears auto_disabled_at) and the
// auto-disable trigger (sets it), neither of which touch url/subscribed_events.
func (r *repository) setWebhookEnabled(
	ctx context.Context, q db.Querier,
	id string, enabled bool, autoDisabledAt *time.Time,
) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.webhook_endpoints
		SET enabled = $2, auto_disabled_at = $3, updated_at = now()
		WHERE id = $1`,
		id, enabled, autoDisabledAt,
	)
	if err != nil {
		return fmt.Errorf("organization.setWebhookEnabled: %w", err)
	}
	return nil
}

func (r *repository) setWebhookHealthWarnedAt(ctx context.Context, q db.Querier, id string, at time.Time) error {
	if _, err := q.Exec(ctx, `UPDATE organization.webhook_endpoints SET health_warned_at = $2 WHERE id = $1`, id, at); err != nil {
		return fmt.Errorf("organization.setWebhookHealthWarnedAt: %w", err)
	}
	return nil
}

func (r *repository) deleteWebhookEndpoint(
	ctx context.Context, q db.Querier,
	organizationID, id string,
) error {
	_, err := q.Exec(ctx, `
		DELETE FROM organization.webhook_endpoints
		WHERE organization_id = $1 AND id = $2`,
		organizationID, id,
	)
	if err != nil {
		return fmt.Errorf("organization.deleteWebhookEndpoint: %w", err)
	}
	return nil
}

// webhookHealth holds the 24h and 3d delivered/total counts for an
// endpoint — backs the list page's health % and the auto-disable/warning checks.
type webhookHealth struct {
	Delivered24h int64
	Total24h     int64
	Delivered3d  int64
	Total3d      int64
}

func (r *repository) webhookHealth(ctx context.Context, q db.Querier, endpointID string) (webhookHealth, error) {
	var h webhookHealth
	err := q.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'delivered' AND created_at > now() - interval '24 hours'),
			COUNT(*) FILTER (WHERE created_at > now() - interval '24 hours'),
			COUNT(*) FILTER (WHERE status = 'delivered' AND created_at > now() - interval '3 days'),
			COUNT(*) FILTER (WHERE created_at > now() - interval '3 days')
		FROM organization.webhook_deliveries WHERE endpoint_id = $1`,
		endpointID,
	).Scan(&h.Delivered24h, &h.Total24h, &h.Delivered3d, &h.Total3d)
	if err != nil {
		return webhookHealth{}, fmt.Errorf("organization.webhookHealth: %w", err)
	}
	return h, nil
}

// webhookHealthBatch is the batched counterpart to webhookHealth — one
// aggregate query for every endpoint instead of one per endpoint, used by
// the list route (webhookHealth itself stays as-is for its other call site,
// a genuine single-endpoint health check after a delivery attempt).
func (r *repository) webhookHealthBatch(ctx context.Context, q db.Querier, endpointIDs []string) (map[string]webhookHealth, error) {
	rows, err := q.Query(ctx, `
		SELECT
			endpoint_id,
			COUNT(*) FILTER (WHERE status = 'delivered' AND created_at > now() - interval '24 hours'),
			COUNT(*) FILTER (WHERE created_at > now() - interval '24 hours'),
			COUNT(*) FILTER (WHERE status = 'delivered' AND created_at > now() - interval '3 days'),
			COUNT(*) FILTER (WHERE created_at > now() - interval '3 days')
		FROM organization.webhook_deliveries
		WHERE endpoint_id = ANY($1)
		GROUP BY endpoint_id`,
		endpointIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.webhookHealthBatch: %w", err)
	}
	defer rows.Close()

	out := make(map[string]webhookHealth, len(endpointIDs))
	for rows.Next() {
		var endpointID string
		var h webhookHealth
		if err := rows.Scan(&endpointID, &h.Delivered24h, &h.Total24h, &h.Delivered3d, &h.Total3d); err != nil {
			return nil, fmt.Errorf("organization.webhookHealthBatch: scan: %w", err)
		}
		out[endpointID] = h
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.webhookHealthBatch: %w", err)
	}
	return out, nil
}
