package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ComponentStatus struct {
	Postgres string `json:"postgres"`
	Redis    string `json:"redis"`
	RabbitMQ string `json:"rabbitmq"`
}

type MemoryStats struct {
	HeapAllocBytes  uint64  `json:"heap_alloc_bytes"`
	HeapSysBytes    uint64  `json:"heap_sys_bytes"`
	StackInUseBytes uint64  `json:"stack_in_use_bytes"`
	GCRuns          uint32  `json:"gc_runs"`
	GCCPUFraction   float64 `json:"gc_cpu_fraction"`
}

type DBPoolStats struct {
	TotalConns    int32 `json:"total_conns"`
	IdleConns     int32 `json:"idle_conns"`
	AcquiredConns int32 `json:"acquired_conns"`
	MaxConns      int32 `json:"max_conns"`
}

type RedisPoolStats struct {
	TotalConns uint32 `json:"total_conns"`
	IdleConns  uint32 `json:"idle_conns"`
	Hits       uint32 `json:"hits"`
	Misses     uint32 `json:"misses"`
}

type RuntimeStats struct {
	UptimeSeconds int64           `json:"uptime_seconds"`
	NumCPU        int             `json:"num_cpu"`
	Goroutines    int             `json:"goroutines"`
	Memory        MemoryStats     `json:"memory"`
	DBPool        DBPoolStats     `json:"db_pool"`
	RedisPool     *RedisPoolStats `json:"redis_pool,omitempty"`
}

type HealthStatus struct {
	Status     string          `json:"status"`     // "ok" | "degraded" | "down"
	LatencyMs  int64           `json:"latency_ms"` // -1 when unreachable
	CheckedAt  string          `json:"checked_at"`
	Message    string          `json:"message"`
	Components ComponentStatus `json:"components"`
	Stats      *RuntimeStats   `json:"stats,omitempty"`
}

type MonitorLog struct {
	ID         int64           `json:"id"`
	ProjectID  string          `json:"project_id"`
	Status     string          `json:"status"`
	LatencyMs  *int64          `json:"latency_ms"`
	CheckedAt  string          `json:"checked_at"`
	Components ComponentStatus `json:"components"`
	Stats      *RuntimeStats   `json:"stats,omitempty"`
}

// pollerHandle carries a generation number alongside its cancel func so a
// poller goroutine's deferred cleanup can tell whether it's still the current
// poller for its projectID before deleting the map entry — otherwise a
// restart racing the old goroutine's cleanup lets the old goroutine wipe out
// the new poller's entry, leaving it running but uncontrollable.
type pollerHandle struct {
	gen    int64
	cancel context.CancelFunc
}

type MonitorService struct {
	db       *sql.DB
	projects *ProjectService
	client   *http.Client
	mu       sync.Mutex
	pollers  map[string]*pollerHandle
	nextGen  int64
}

func NewMonitorService(db *sql.DB, projects *ProjectService) *MonitorService {
	return &MonitorService{
		db:       db,
		projects: projects,
		client:   &http.Client{Timeout: 10 * time.Second},
		pollers:  make(map[string]*pollerHandle),
	}
}

func (s *MonitorService) CheckHealth(projectID string) (HealthStatus, error) {
	project, err := s.projects.Get(projectID)
	if err != nil {
		return HealthStatus{}, err
	}

	base := healthBaseURL(project.APIURL)
	now := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/ready", nil)
	if err != nil {
		s.insertLog(projectID, "down", nil, "", "")
		return HealthStatus{Status: "down", LatencyMs: -1, CheckedAt: now.Format(time.RFC3339), Message: err.Error()}, nil
	}

	resp, err := s.client.Do(req)
	latencyMs := time.Since(now).Milliseconds()

	if err != nil {
		s.insertLog(projectID, "down", nil, "", "")
		return HealthStatus{Status: "down", LatencyMs: -1, CheckedAt: now.Format(time.RFC3339), Message: err.Error()}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	components := parseComponents(body)
	status, msg := classifyResponse(resp.StatusCode, latencyMs)

	stats := s.fetchStats(base, project.StatsToken)

	compJSON, _ := json.Marshal(components)
	statsJSON, _ := json.Marshal(stats)
	s.insertLog(projectID, status, &latencyMs, string(compJSON), string(statsJSON))

	return HealthStatus{
		Status:     status,
		LatencyMs:  latencyMs,
		CheckedAt:  now.Format(time.RFC3339),
		Message:    msg,
		Components: components,
		Stats:      stats,
	}, nil
}

func (s *MonitorService) GetHistory(projectID string, limit int) ([]MonitorLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.db.Query(`
		SELECT id, project_id, status, latency_ms, checked_at,
		       COALESCE(components, '{}'), COALESCE(stats, 'null')
		FROM monitor_log
		WHERE project_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []MonitorLog
	for rows.Next() {
		var l MonitorLog
		var compJSON, statsJSON string
		if err := rows.Scan(&l.ID, &l.ProjectID, &l.Status, &l.LatencyMs, &l.CheckedAt, &compJSON, &statsJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(compJSON), &l.Components) //nolint:errcheck
		json.Unmarshal([]byte(statsJSON), &l.Stats)     //nolint:errcheck
		logs = append(logs, l)
	}
	return logs, nil
}

func (s *MonitorService) GetLastStatus(projectID string) (*MonitorLog, error) {
	var l MonitorLog
	var compJSON, statsJSON string
	err := s.db.QueryRow(`
		SELECT id, project_id, status, latency_ms, checked_at,
		       COALESCE(components, '{}'), COALESCE(stats, 'null')
		FROM monitor_log
		WHERE project_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, projectID).Scan(&l.ID, &l.ProjectID, &l.Status, &l.LatencyMs, &l.CheckedAt, &compJSON, &statsJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(compJSON), &l.Components) //nolint:errcheck
	json.Unmarshal([]byte(statsJSON), &l.Stats)     //nolint:errcheck
	return &l, nil
}

func (s *MonitorService) IsPolling(projectID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pollers[projectID]
	return ok
}

func (s *MonitorService) StartPoller(projectID string, intervalSeconds int) error {
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	s.mu.Lock()
	if h, ok := s.pollers[projectID]; ok {
		h.cancel()
	}
	s.nextGen++
	gen := s.nextGen
	ctx, cancel := context.WithCancel(context.Background())
	s.pollers[projectID] = &pollerHandle{gen: gen, cancel: cancel}
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			if h, ok := s.pollers[projectID]; ok && h.gen == gen {
				delete(s.pollers, projectID)
			}
			s.mu.Unlock()
		}()

		s.pollCheck(ctx, projectID)

		ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.pollCheck(ctx, projectID)
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (s *MonitorService) StopPoller(projectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.pollers[projectID]; ok {
		h.cancel()
		delete(s.pollers, projectID)
	}
}

func (s *MonitorService) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, h := range s.pollers {
		h.cancel()
		delete(s.pollers, id)
	}
}

func (s *MonitorService) pollCheck(ctx context.Context, projectID string) {
	project, err := s.projects.Get(projectID)
	if err != nil {
		return
	}

	base := healthBaseURL(project.APIURL)
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/ready", nil)
	if err != nil {
		s.insertLog(projectID, "down", nil, "", "")
		return
	}

	resp, err := s.client.Do(req)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		s.insertLog(projectID, "down", nil, "", "")
		return
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	components := parseComponents(body)
	status, _ := classifyResponse(resp.StatusCode, latencyMs)
	stats := s.fetchStats(base, project.StatsToken)

	compJSON, _ := json.Marshal(components)
	statsJSON, _ := json.Marshal(stats)
	s.insertLog(projectID, status, &latencyMs, string(compJSON), string(statsJSON))
}

// fetchStats sends statsToken as X-Stats-Token when set — matching the
// project API's optional StatsToken gate on /health/stats. An empty token
// still works against an API that hasn't configured one.
func (s *MonitorService) fetchStats(base, statsToken string) *RuntimeStats {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/stats", nil)
	if err != nil {
		return nil
	}
	if statsToken != "" {
		req.Header.Set("X-Stats-Token", statsToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	var stats RuntimeStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil
	}
	return &stats
}

func (s *MonitorService) insertLog(projectID, status string, latencyMs *int64, components, stats string) {
	_, err := s.db.Exec(`
		INSERT INTO monitor_log (project_id, status, latency_ms, components, stats)
		VALUES (?, ?, ?, ?, ?)
	`, projectID, status, latencyMs, components, stats)
	if err != nil {
		log.Printf("MonitorService.insertLog: %v", err)
	}
}

func healthBaseURL(apiURL string) string {
	u := strings.TrimRight(apiURL, "/")
	for _, suffix := range []string{"/api/v1", "/api"} {
		if strings.HasSuffix(u, suffix) {
			return strings.TrimSuffix(u, suffix)
		}
	}
	return u
}

func parseComponents(body []byte) ComponentStatus {
	var readyBody struct {
		Checks map[string]string `json:"checks"`
	}
	if json.Unmarshal(body, &readyBody) == nil && readyBody.Checks != nil {
		return ComponentStatus{
			Postgres: readyBody.Checks["postgres"],
			Redis:    readyBody.Checks["redis"],
			RabbitMQ: readyBody.Checks["rabbitmq"],
		}
	}
	return ComponentStatus{}
}

func classifyResponse(code int, latencyMs int64) (string, string) {
	if code == http.StatusServiceUnavailable {
		return "degraded", ""
	}
	if code != http.StatusOK {
		return "down", ""
	}
	if latencyMs >= 500 {
		return "degraded", ""
	}
	return "ok", ""
}
