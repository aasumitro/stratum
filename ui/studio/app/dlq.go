package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DLQInfo struct {
	Name        string  `json:"name"`
	Messages    int     `json:"messages"`
	Consumers   int     `json:"consumers"`
	State       string  `json:"state"`
	MessageRate float64 `json:"message_rate"` // messages/sec (incoming publish rate)
}

type DLQMessage struct {
	RoutingKey  string `json:"routing_key"`
	Exchange    string `json:"exchange"`
	Body        string `json:"body"`
	BodyBytes   int    `json:"body_bytes"`
	Redelivered bool   `json:"redelivered"`
}

// RequeueResult reports the outcome of RequeueMessage. When Requeued is false,
// Lost holds the message that was consumed from the DLQ but never made it back
// onto its exchange — the operator's only remaining record of it — and Error
// explains why the republish failed.
type RequeueResult struct {
	Requeued bool        `json:"requeued"`
	Lost     *DLQMessage `json:"lost,omitempty"`
	Error    string      `json:"error,omitempty"`
}

type mqConfig struct {
	apiBase string
	vhost   string
	user    string
	pass    string
}

func parseMQConfig(amqpDSN string) (mqConfig, error) {
	u, err := url.Parse(amqpDSN)
	if err != nil {
		return mqConfig{}, fmt.Errorf("parseMQConfig: %w", err)
	}

	scheme := "http"
	mgmtPort := "15672"
	if u.Scheme == "amqps" {
		scheme = "https"
		mgmtPort = "15671"
	}

	host := u.Hostname()
	if host == "" {
		host = "localhost"
	}

	// Derive vhost: strip leading slash; empty or "/" → default vhost "%2F"
	vhostRaw := strings.TrimPrefix(u.Path, "/")
	if vhostRaw == "" {
		vhostRaw = "/"
	}
	vhost := url.PathEscape(vhostRaw)

	user, pass := "guest", "guest"
	if u.User != nil {
		user = u.User.Username()
		if p, ok := u.User.Password(); ok {
			pass = p
		}
	}

	return mqConfig{
		apiBase: fmt.Sprintf("%s://%s:%s/api", scheme, host, mgmtPort),
		vhost:   vhost,
		user:    user,
		pass:    pass,
	}, nil
}

type DLQService struct {
	projects *ProjectService
	pool     *connect.PostgresPool
	client   *http.Client
}

func NewDLQService(projects *ProjectService, pool *connect.PostgresPool) *DLQService {
	return &DLQService{
		projects: projects,
		pool:     pool,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// recordAction writes the audit row for a broker change. The broker cannot
// attribute anything to a person, because its credentials are shared, so callers
// run this first and do not touch the broker if it fails. The row therefore records
// an attempt; recordFailure adds a second row when the attempt does not work out.
func (s *DLQService) recordAction(projectID, action, queue string, metadata map[string]any) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "DLQService.recordAction", 10*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.RecordOperatorAction(ctx, db, action, queue, metadata)
		})
}

// recordFailure lets the audit trail tell a failed attempt from one that worked.
// The broker call has already happened by now, so a failure here can only be logged.
func (s *DLQService) recordFailure(projectID, action, queue string, cause error) {
	metadata := map[string]any{"error": connect.RedactDSN(cause.Error())}
	if err := s.recordAction(projectID, action, queue, metadata); err != nil {
		log.Printf("DLQService.recordFailure: %v", err)
	}
}

func (s *DLQService) request(ctx context.Context, cfg mqConfig, method, path string, body any) ([]byte, error) {
	var rb io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rb = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfg.apiBase+path, rb)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.user, cfg.pass)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("management API %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (s *DLQService) getConfig(projectID string) (mqConfig, error) {
	project, err := s.projects.Get(projectID)
	if err != nil {
		return mqConfig{}, err
	}
	return parseMQConfig(project.MQDSN)
}

func (s *DLQService) ListQueues(projectID string) ([]DLQInfo, error) {
	cfg, err := s.getConfig(projectID)
	if err != nil {
		return nil, fmt.Errorf("DLQService.ListQueues: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	data, err := s.request(ctx, cfg, http.MethodGet,
		"/queues?columns=name,messages,consumers,state,message_stats.publish_details.rate", nil)
	if err != nil {
		return nil, fmt.Errorf("DLQService.ListQueues: %w", err)
	}

	// RabbitMQ returns message_stats as [] (empty array) when a queue has never
	// received messages, so we can't decode it as a struct directly.
	var raw []struct {
		Name         string          `json:"name"`
		Messages     int             `json:"messages"`
		Consumers    int             `json:"consumers"`
		State        string          `json:"state"`
		MessageStats json.RawMessage `json:"message_stats"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("DLQService.ListQueues: decode: %w", err)
	}

	out := make([]DLQInfo, len(raw))
	for i, q := range raw {
		var rate float64
		if len(q.MessageStats) > 0 && q.MessageStats[0] == '{' {
			var stats struct {
				PublishDetails struct {
					Rate float64 `json:"rate"`
				} `json:"publish_details"`
			}
			_ = json.Unmarshal(q.MessageStats, &stats)
			rate = stats.PublishDetails.Rate
		}
		out[i] = DLQInfo{
			Name:        q.Name,
			Messages:    q.Messages,
			Consumers:   q.Consumers,
			State:       q.State,
			MessageRate: rate,
		}
	}
	return out, nil
}

func (s *DLQService) GetMessages(projectID, queue string, limit int) ([]DLQMessage, error) {
	cfg, err := s.getConfig(projectID)
	if err != nil {
		return nil, fmt.Errorf("DLQService.GetMessages: %w", err)
	}

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	data, err := s.request(ctx, cfg, http.MethodPost,
		fmt.Sprintf("/queues/%s/%s/get", cfg.vhost, url.PathEscape(queue)),
		map[string]any{
			"count":    limit,
			"ackmode":  "ack_requeue_true", // peek — does NOT remove messages
			"encoding": "auto",
			"truncate": 50000,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("DLQService.GetMessages: %w", err)
	}

	var raw []struct {
		RoutingKey   string `json:"routing_key"`
		Exchange     string `json:"exchange"`
		Payload      string `json:"payload"`
		PayloadBytes int    `json:"payload_bytes"`
		Redelivered  bool   `json:"redelivered"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("DLQService.GetMessages: decode: %w", err)
	}

	out := make([]DLQMessage, len(raw))
	for i, m := range raw {
		out[i] = DLQMessage{
			RoutingKey:  m.RoutingKey,
			Exchange:    m.Exchange,
			Body:        m.Payload,
			BodyBytes:   m.PayloadBytes,
			Redelivered: m.Redelivered,
		}
	}
	return out, nil
}

// RequeueMessage consumes the next message from the DLQ and republishes it to
// its original exchange using x-death headers. Always operates FIFO (front of queue).
//
// The consume and republish are two separate, unlinked API calls. If republish
// fails after the message has already been consumed (removed from the DLQ), the
// returned RequeueResult carries the consumed message (Requeued: false, Lost: ...)
// instead of losing it silently — the error return is reserved for failures
// before anything was consumed, where there is nothing to recover.
func (s *DLQService) RequeueMessage(projectID, queue string) (result *RequeueResult, err error) {
	if err := requireOperator(s.projects, s.pool, projectID); err != nil {
		return nil, err
	}

	cfg, err := s.getConfig(projectID)
	if err != nil {
		return nil, fmt.Errorf("DLQService.RequeueMessage: %w", err)
	}

	if err := s.recordAction(projectID, "studio.dlq.requeue", queue, map[string]any{"count": 1}); err != nil {
		return nil, fmt.Errorf("DLQService.RequeueMessage: %w", err)
	}
	defer func() {
		if err != nil {
			s.recordFailure(projectID, "studio.dlq.requeue.failed", queue, err)
		} else if !result.Requeued {
			s.recordFailure(projectID, "studio.dlq.requeue.failed", queue, errors.New(result.Error))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Consume one message from the DLQ (removes from queue)
	data, err := s.request(ctx, cfg, http.MethodPost,
		fmt.Sprintf("/queues/%s/%s/get", cfg.vhost, url.PathEscape(queue)),
		map[string]any{
			"count":    1,
			"ackmode":  "ack_requeue_false",
			"encoding": "auto",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("DLQService.RequeueMessage: consume: %w", err)
	}

	var msgs []struct {
		RoutingKey string `json:"routing_key"`
		Exchange   string `json:"exchange"`
		Payload    string `json:"payload"`
		// The management API sends properties as [] for a message that has none,
		// so it cannot decode straight into a struct. A failure here would come
		// after the message already left the queue, and would lose it.
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("DLQService.RequeueMessage: decode: %w", err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("DLQService.RequeueMessage: queue is empty")
	}

	msg := msgs[0]
	targetExchange := msg.Exchange
	targetRoutingKey := msg.RoutingKey

	var properties struct {
		Headers map[string]any `json:"headers"`
	}
	if len(msg.Properties) > 0 && msg.Properties[0] == '{' {
		_ = json.Unmarshal(msg.Properties, &properties)
	}

	// Extract original destination from x-death headers if present
	if xDeath, ok := properties.Headers["x-death"]; ok {
		if deaths, ok := xDeath.([]any); ok && len(deaths) > 0 {
			if death, ok := deaths[0].(map[string]any); ok {
				if ex, ok := death["exchange"].(string); ok && ex != "" {
					targetExchange = ex
				}
				if rks, ok := death["routing-keys"].([]any); ok && len(rks) > 0 {
					if rk, ok := rks[0].(string); ok {
						targetRoutingKey = rk
					}
				}
			}
		}
	}

	// Republish via Management API exchange publish endpoint
	_, err = s.request(ctx, cfg, http.MethodPost,
		fmt.Sprintf("/exchanges/%s/%s/publish", cfg.vhost, url.PathEscape(targetExchange)),
		map[string]any{
			"routing_key":      targetRoutingKey,
			"payload":          msg.Payload,
			"payload_encoding": "string",
			"properties":       map[string]any{},
		},
	)
	if err != nil {
		// Already consumed from the DLQ — return it as a non-error result so the
		// caller can show the operator exactly what was lost and let them
		// republish manually, instead of the payload vanishing with the error.
		return &RequeueResult{
			Requeued: false,
			Lost: &DLQMessage{
				RoutingKey: targetRoutingKey,
				Exchange:   targetExchange,
				Body:       msg.Payload,
				BodyBytes:  len(msg.Payload),
			},
			Error: fmt.Errorf("DLQService.RequeueMessage: republish: %w", err).Error(),
		}, nil
	}

	return &RequeueResult{Requeued: true}, nil
}

func (s *DLQService) PurgeQueue(projectID, queue string) (purged int, err error) {
	if err := requireOperator(s.projects, s.pool, projectID); err != nil {
		return 0, err
	}

	cfg, err := s.getConfig(projectID)
	if err != nil {
		return 0, fmt.Errorf("DLQService.PurgeQueue: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Fetch count before purging so we can report how many were removed
	qData, err := s.request(ctx, cfg, http.MethodGet,
		fmt.Sprintf("/queues/%s/%s", cfg.vhost, url.PathEscape(queue)), nil)
	if err != nil {
		return 0, fmt.Errorf("DLQService.PurgeQueue: get info: %w", err)
	}
	var qInfo struct {
		Messages int `json:"messages"`
	}
	_ = json.Unmarshal(qData, &qInfo)

	if err := s.recordAction(projectID, "studio.dlq.purge", queue, map[string]any{"count": qInfo.Messages}); err != nil {
		return 0, fmt.Errorf("DLQService.PurgeQueue: %w", err)
	}

	if _, err := s.request(ctx, cfg, http.MethodDelete,
		fmt.Sprintf("/queues/%s/%s/contents", cfg.vhost, url.PathEscape(queue)), nil); err != nil {
		wrapped := fmt.Errorf("DLQService.PurgeQueue: %w", err)
		s.recordFailure(projectID, "studio.dlq.purge.failed", queue, wrapped)
		return 0, wrapped
	}

	return qInfo.Messages, nil
}
