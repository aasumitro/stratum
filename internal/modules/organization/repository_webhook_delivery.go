package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

// ── Webhook deliveries ─────────────────────────────────────────────────────

type webhookDeliveryRecord struct {
	ID                 string          `json:"id"`
	EndpointID         string          `json:"endpoint_id"`
	EventID            string          `json:"event_id"`
	EventType          string          `json:"event_type"`
	Status             string          `json:"status"`
	Attempts           int             `json:"attempts"`
	LastError          *string         `json:"last_error,omitempty"`
	ResponseStatusCode *int            `json:"response_status_code,omitempty"`
	ResponseBody       *string         `json:"response_body,omitempty"`
	LatencyMS          *int            `json:"latency_ms,omitempty"`
	AttemptsLog        json.RawMessage `json:"attempts_log"`
	DeliveredAt        *time.Time      `json:"delivered_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}

const webhookDeliveryColumns = `id, endpoint_id, event_id, event_type, status, attempts, last_error,
	response_status_code, response_body, latency_ms, attempts_log, delivered_at, created_at`

func scanWebhookDelivery(row interface{ Scan(dest ...any) error }, rec *webhookDeliveryRecord) error {
	return row.Scan(
		&rec.ID, &rec.EndpointID, &rec.EventID, &rec.EventType, &rec.Status, &rec.Attempts, &rec.LastError,
		&rec.ResponseStatusCode, &rec.ResponseBody, &rec.LatencyMS, &rec.AttemptsLog, &rec.DeliveredAt, &rec.CreatedAt,
	)
}

func (r *repository) insertWebhookDelivery(
	ctx context.Context, q db.Querier,
	endpointID, eventID, eventType string,
) (*webhookDeliveryRecord, error) {
	rec := new(webhookDeliveryRecord)
	err := scanWebhookDelivery(q.QueryRow(ctx, `
		INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type)
		VALUES ($1, $2, $3)
		RETURNING `+webhookDeliveryColumns,
		endpointID, eventID, eventType,
	), rec)
	return rec, err
}

// recordWebhookAttempt appends one entry to attempts_log and updates the
// delivery's summary columns — called once per delivery attempt (initial +
// each retry), so the frontend's timeline can render every attempt.
func (r *repository) recordWebhookAttempt(
	ctx context.Context, q db.Querier,
	id, status string, attempts int, lastError *string,
	statusCode *int, responseBody *string, latencyMS *int,
	deliveredAt *time.Time, logEntry json.RawMessage,
) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.webhook_deliveries
		SET status = $2, attempts = $3, last_error = $4, response_status_code = $5,
		    response_body = $6, latency_ms = $7, delivered_at = $8,
		    attempts_log = attempts_log || $9::jsonb
		WHERE id = $1`,
		id, status, attempts, lastError, statusCode, responseBody, latencyMS, deliveredAt, logEntry,
	)
	return err
}

type webhookDeliveryFilter struct {
	Status    string
	EventType string
	Since     *time.Time
}

// listWebhookDeliveries paginates by created_at (not id —
// organization.webhook_deliveries.id is gen_random_uuid(), not
// time-ordered, so "id < cursor ORDER BY id DESC" wouldn't mean
// newest-first). Same cursor convention as
// internal/platform/audit.ListByActorCursor.
//
// Deliberately keys the cursor on created_at alone, not (created_at, id) —
// two deliveries landing in the same nanosecond could skip/duplicate one
// row at a page boundary. Fine for a delivery timeline; use a composite
// keyset cursor if this ever needs stronger pagination guarantees.
func (r *repository) listWebhookDeliveries(
	ctx context.Context, q db.Querier,
	endpointID string, filter webhookDeliveryFilter,
	cursor string, limit int,
) ([]webhookDeliveryRecord, string, error) {
	conds := []string{"endpoint_id = $1"}
	args := db.NewArgs(endpointID)
	if filter.Status != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", args.Add(filter.Status)))
	}
	if filter.EventType != "" {
		conds = append(conds, fmt.Sprintf("event_type = $%d", args.Add(filter.EventType)))
	}
	if filter.Since != nil {
		conds = append(conds, fmt.Sprintf("created_at > $%d", args.Add(*filter.Since)))
	}
	if cursor != "" {
		ts, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		conds = append(conds, fmt.Sprintf("created_at < $%d", args.Add(ts)))
	}

	query := `SELECT ` + webhookDeliveryColumns + ` FROM organization.webhook_deliveries WHERE ` +
		strings.Join(conds, " AND ") + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", args.Add(limit))

	rows, err := q.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	recs := make([]webhookDeliveryRecord, 0, limit)
	for rows.Next() {
		var rec webhookDeliveryRecord
		if err := scanWebhookDelivery(rows, &rec); err != nil {
			return nil, "", err
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(recs) == limit {
		nextCursor = recs[len(recs)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return recs, nextCursor, nil
}

// listFailedWebhookDeliveries backs "Retry all failed" — every failed
// delivery for the endpoint, capped at 50 (each is dispatched as its own
// async retry job; a single bulk click shouldn't be able to queue an
// unbounded amount of outbound work).
func (r *repository) listFailedWebhookDeliveries(
	ctx context.Context, q db.Querier, endpointID string,
) ([]webhookDeliveryRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT `+webhookDeliveryColumns+`
		FROM organization.webhook_deliveries
		WHERE endpoint_id = $1 AND status = 'failed'
		ORDER BY created_at DESC LIMIT 50`,
		endpointID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	recs := []webhookDeliveryRecord{}
	for rows.Next() {
		var rec webhookDeliveryRecord
		if err := scanWebhookDelivery(rows, &rec); err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	return recs, rows.Err()
}

func (r *repository) findWebhookDelivery(
	ctx context.Context, q db.Querier,
	deliveryID string,
) (*webhookDeliveryRecord, error) {
	rec := new(webhookDeliveryRecord)
	err := scanWebhookDelivery(q.QueryRow(ctx, `
		SELECT `+webhookDeliveryColumns+`
		FROM organization.webhook_deliveries
		WHERE id = $1`,
		deliveryID,
	), rec)
	return rec, err
}
