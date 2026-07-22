package app

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

// These tests hit the real OS keychain (macOS Keychain / Windows Credential
// Manager / Linux Secret Service) — there is no in-process fake for it.
func TestProjectSecrets_RoundTrip(t *testing.T) {
	id := "secret-test-" + t.Name()
	t.Cleanup(func() { _ = deleteProjectSecrets(id) })

	want := projectSecrets{DB: "postgres://u:p@host/db", MQ: "amqp://u:p@host/", Redis: "redis://host:6379"}
	if err := setProjectSecrets(id, want); err != nil {
		t.Fatalf("setProjectSecrets: %v", err)
	}

	got, err := getProjectSecrets(id)
	if err != nil {
		t.Fatalf("getProjectSecrets: %v", err)
	}
	if got != want {
		t.Errorf("getProjectSecrets = %+v, want %+v", got, want)
	}
}

func TestProjectSecrets_Delete(t *testing.T) {
	id := "secret-test-" + t.Name()

	if err := setProjectSecrets(id, projectSecrets{DB: "x"}); err != nil {
		t.Fatalf("setProjectSecrets: %v", err)
	}
	if err := deleteProjectSecrets(id); err != nil {
		t.Fatalf("deleteProjectSecrets: %v", err)
	}

	if _, err := getProjectSecrets(id); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("getProjectSecrets after delete: got err %v, want ErrNotFound", err)
	}
}

func TestProjectSecrets_DeleteMissingIsNoop(t *testing.T) {
	if err := deleteProjectSecrets("secret-test-never-existed"); err != nil {
		t.Errorf("deleteProjectSecrets on missing entry: %v", err)
	}
}
