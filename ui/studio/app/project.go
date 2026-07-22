package app

import (
	"crypto/rand"
	"database/sql"
	"fmt"
)

type Project struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	APIURL     string  `json:"api_url"`
	DBDSN      string  `json:"db_dsn"`
	MQDSN      string  `json:"mq_dsn"`
	RedisDSN   string  `json:"redis_dsn"`
	StatsToken string  `json:"stats_token"`
	Color      string  `json:"color"`
	CreatedAt  string  `json:"created_at"`
	LastSeenAt *string `json:"last_seen_at"`
}

type ProjectInput struct {
	Name       string `json:"name"`
	APIURL     string `json:"api_url"`
	DBDSN      string `json:"db_dsn"`
	MQDSN      string `json:"mq_dsn"`
	RedisDSN   string `json:"redis_dsn"`
	StatsToken string `json:"stats_token"`
	Color      string `json:"color"`
}

type ProjectService struct {
	db *sql.DB
}

func NewProjectService(db *sql.DB) *ProjectService {
	return &ProjectService{db: db}
}

func (s *ProjectService) List() ([]Project, error) {
	rows, err := s.db.Query(`
		SELECT id, name, api_url, db_dsn, mq_dsn,
		       COALESCE(redis_dsn, '') AS redis_dsn,
		       color, created_at, last_seen_at
		FROM projects
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("ProjectService.List: %w", err)
	}

	var projects []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("ProjectService.List: scan: %w", err)
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("ProjectService.List: rows: %w", err)
	}
	rows.Close()

	// resolveSecrets may write (migrating a legacy plaintext row), so it must
	// run after the read cursor above is closed — SQLite allows only one
	// writer at a time, and a nested write while rows is still open
	// self-deadlocks as SQLITE_BUSY.
	for i := range projects {
		if err := s.resolveSecrets(&projects[i]); err != nil {
			return nil, fmt.Errorf("ProjectService.List: %w", err)
		}
	}
	return projects, nil
}

func (s *ProjectService) Get(id string) (Project, error) {
	row := s.db.QueryRow(`
		SELECT id, name, api_url, db_dsn, mq_dsn,
		       COALESCE(redis_dsn, '') AS redis_dsn,
		       color, created_at, last_seen_at
		FROM projects WHERE id = ?
	`, id)
	p, err := scanProject(row)
	if err != nil {
		return Project{}, fmt.Errorf("ProjectService.Get: %w", err)
	}
	if err := s.resolveSecrets(&p); err != nil {
		return Project{}, fmt.Errorf("ProjectService.Get: %w", err)
	}
	return p, nil
}

// resolveSecrets fills in the DSN fields from the OS keychain. A row whose
// DSN columns still hold plaintext (written before the keychain migration)
// is migrated in place: the plaintext is pushed into the keychain and the
// columns are blanked, so every row converges to keychain-backed storage
// the first time it's read.
func (s *ProjectService) resolveSecrets(p *Project) error {
	if p.DBDSN != "" || p.MQDSN != "" || p.RedisDSN != "" {
		if err := setProjectSecrets(p.ID, projectSecrets{DB: p.DBDSN, MQ: p.MQDSN, Redis: p.RedisDSN}); err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE projects SET db_dsn = '', mq_dsn = '', redis_dsn = NULL WHERE id = ?`, p.ID); err != nil {
			return fmt.Errorf("clear legacy columns: %w", err)
		}
		return nil
	}

	secrets, err := getProjectSecrets(p.ID)
	if err != nil {
		return err
	}
	p.DBDSN = secrets.DB
	p.MQDSN = secrets.MQ
	p.RedisDSN = secrets.Redis
	p.StatsToken = secrets.StatsToken
	return nil
}

func (s *ProjectService) Add(input ProjectInput) (Project, error) {
	id, err := newID()
	if err != nil {
		return Project{}, fmt.Errorf("ProjectService.Add: %w", err)
	}
	if err := setProjectSecrets(id, projectSecrets{DB: input.DBDSN, MQ: input.MQDSN, Redis: input.RedisDSN, StatsToken: input.StatsToken}); err != nil {
		return Project{}, fmt.Errorf("ProjectService.Add: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO projects (id, name, api_url, db_dsn, mq_dsn, redis_dsn, color)
		VALUES (?, ?, ?, '', '', NULL, ?)
	`, id, input.Name, input.APIURL, input.Color)
	if err != nil {
		return Project{}, fmt.Errorf("ProjectService.Add: %w", err)
	}
	return s.Get(id)
}

func (s *ProjectService) Update(id string, input ProjectInput) (Project, error) {
	if err := setProjectSecrets(id, projectSecrets{DB: input.DBDSN, MQ: input.MQDSN, Redis: input.RedisDSN, StatsToken: input.StatsToken}); err != nil {
		return Project{}, fmt.Errorf("ProjectService.Update: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE projects
		SET name = ?, api_url = ?, db_dsn = '', mq_dsn = '', redis_dsn = NULL, color = ?
		WHERE id = ?
	`, input.Name, input.APIURL, input.Color, id)
	if err != nil {
		return Project{}, fmt.Errorf("ProjectService.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Project{}, fmt.Errorf("ProjectService.Update: project %s not found", id)
	}
	return s.Get(id)
}

func (s *ProjectService) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("ProjectService.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("ProjectService.Delete: project %s not found", id)
	}
	if err := deleteProjectSecrets(id); err != nil {
		return fmt.Errorf("ProjectService.Delete: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanProject(s scanner) (Project, error) {
	var p Project
	err := s.Scan(
		&p.ID, &p.Name, &p.APIURL, &p.DBDSN, &p.MQDSN, &p.RedisDSN,
		&p.Color, &p.CreatedAt, &p.LastSeenAt,
	)
	return p, err
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("newID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
