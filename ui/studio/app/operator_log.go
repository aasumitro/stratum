package app

import (
	"database/sql"
	"fmt"
)

type OperatorLogEntry struct {
	ID          int64  `json:"id"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Action      string `json:"action"`
	TargetID    string `json:"target_id"`
	Detail      string `json:"detail"`
	PerformedAt string `json:"performed_at"`
}

type OperatorLogService struct {
	db *sql.DB
}

func NewOperatorLogService(db *sql.DB) *OperatorLogService {
	return &OperatorLogService{db: db}
}

// List returns paginated operator log entries. Pass projectID="" to list across all projects.
func (s *OperatorLogService) List(projectID string, limit, offset int) ([]OperatorLogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.db.Query(`
		SELECT o.id, o.project_id, COALESCE(p.name, '') AS project_name,
		       o.action, o.target_id, COALESCE(o.detail, '') AS detail,
		       o.performed_at
		FROM operator_log o
		LEFT JOIN projects p ON p.id = o.project_id
		WHERE o.project_id = ? OR ? = ''
		ORDER BY o.performed_at DESC
		LIMIT ? OFFSET ?
	`, projectID, projectID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("OperatorLogService.List: %w", err)
	}
	defer rows.Close()

	var entries []OperatorLogEntry
	for rows.Next() {
		var e OperatorLogEntry
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.ProjectName, &e.Action, &e.TargetID, &e.Detail, &e.PerformedAt); err != nil {
			return nil, fmt.Errorf("OperatorLogService.List scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("OperatorLogService.List rows: %w", err)
	}

	return entries, nil
}

// Count returns the total number of log entries for the given project (or all projects when projectID="").
func (s *OperatorLogService) Count(projectID string) (int, error) {
	var total int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM operator_log
		WHERE project_id = ? OR ? = ''
	`, projectID, projectID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("OperatorLogService.Count: %w", err)
	}
	return total, nil
}
