package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BroadcastInput struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Target string `json:"target"` // "all" | "organization:<uuid>" | "user:<auth_sub>"
}

type BroadcastLog struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	Target         string `json:"target"`
	RecipientCount int    `json:"recipient_count"`
	SentAt         string `json:"sent_at"`
}

type BroadcastService struct {
	db       *sql.DB
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewBroadcastService(db *sql.DB, projects *ProjectService, pool *connect.PostgresPool) *BroadcastService {
	return &BroadcastService{db: db, projects: projects, pool: pool}
}

// CountRecipients returns the number of users who would receive a broadcast with the given target.
func (s *BroadcastService) CountRecipients(projectID, target string) (int, error) {
	return withProjectDB(s.projects, s.pool, projectID, "BroadcastService.CountRecipients", 10*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) (int, error) {
			return query.CountBroadcastRecipients(ctx, db, target)
		})
}

// broadcastBatchSize caps how many recipients are inserted under one context
// deadline, so a large "all" broadcast gets a fresh timeout per batch instead
// of racing a single fixed deadline for the whole send.
const broadcastBatchSize = 200

// Send resolves recipients for the target, inserts in-app notification messages in the target
// project DB, and writes the broadcast to the local operator log.
// Returns the number of recipients who received the notification.
func (s *BroadcastService) Send(projectID string, input BroadcastInput) (int, error) {
	pg, err := withProjectPool(s.projects, s.pool, projectID, "BroadcastService.Send")
	if err != nil {
		return 0, err
	}

	resolveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	recipients, err := query.ResolveBroadcastRecipients(resolveCtx, pg, input.Target)
	cancel()
	if err != nil {
		return 0, fmt.Errorf("BroadcastService.Send: resolve: %w", err)
	}

	var sent int
	for i := 0; i < len(recipients); i += broadcastBatchSize {
		batch := recipients[i:min(i+broadcastBatchSize, len(recipients))]

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		for _, r := range batch {
			if err := query.InsertBroadcastNotification(ctx, pg, r, input.Title, input.Body); err != nil {
				log.Printf("BroadcastService.Send: insert for %s: %v", r.AuthSub, err)
				continue
			}
			sent++
		}
		cancel()
	}

	s.logBroadcast(projectID, input, sent)
	return sent, nil
}

// GetHistory returns the broadcast log for a project from the local SQLite store.
func (s *BroadcastService) GetHistory(projectID string) ([]BroadcastLog, error) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT id, title, body, target, recipient_count, sent_at
		FROM broadcast_log
		WHERE project_id = ?
		ORDER BY sent_at DESC
		LIMIT 100
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("BroadcastService.GetHistory: %w", err)
	}
	defer rows.Close()

	var out []BroadcastLog
	for rows.Next() {
		var l BroadcastLog
		if err := rows.Scan(&l.ID, &l.Title, &l.Body, &l.Target, &l.RecipientCount, &l.SentAt); err != nil {
			return nil, fmt.Errorf("BroadcastService.GetHistory: scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *BroadcastService) logBroadcast(projectID string, input BroadcastInput, count int) {
	_, err := s.db.ExecContext(context.Background(), `
		INSERT INTO broadcast_log (project_id, title, body, target, recipient_count)
		VALUES (?, ?, ?, ?, ?)
	`, projectID, input.Title, input.Body, input.Target, count)
	if err != nil {
		log.Printf("BroadcastService.logBroadcast: %v", err)
	}
}
