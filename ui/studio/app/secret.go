package app

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keyringService namespaces Studio's entries in the OS keychain
// (macOS Keychain / Windows Credential Manager / Linux Secret Service).
const keyringService = "stratum-studio"

// projectSecrets is the DSN bundle stored under one keychain entry per
// project. StatsToken is the project API's STATS_TOKEN — sent back as
// X-Stats-Token when polling /health/stats (see health.go's StatsToken gate
// on the API side).
type projectSecrets struct {
	DB         string `json:"db"`
	MQ         string `json:"mq"`
	Redis      string `json:"redis"`
	StatsToken string `json:"stats_token"`
}

func setProjectSecrets(projectID string, s projectSecrets) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("setProjectSecrets: %w", err)
	}
	if err := keyring.Set(keyringService, projectID, string(b)); err != nil {
		return fmt.Errorf("setProjectSecrets: %w", err)
	}
	return nil
}

func getProjectSecrets(projectID string) (projectSecrets, error) {
	raw, err := keyring.Get(keyringService, projectID)
	if err != nil {
		return projectSecrets{}, fmt.Errorf("getProjectSecrets: %w", err)
	}
	var s projectSecrets
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return projectSecrets{}, fmt.Errorf("getProjectSecrets: %w", err)
	}
	return s, nil
}

// deleteProjectSecrets is idempotent: a missing entry is not an error.
func deleteProjectSecrets(projectID string) error {
	err := keyring.Delete(keyringService, projectID)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("deleteProjectSecrets: %w", err)
	}
	return nil
}
