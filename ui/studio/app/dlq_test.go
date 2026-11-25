package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckOwnBrokerLogin(t *testing.T) {
	for name, c := range map[string]struct {
		identity brokerIdentity
		want     error
	}{
		"own login":                   {brokerIdentity{Name: "op_ana", Tags: []string{"management"}}, nil},
		"another person's":            {brokerIdentity{Name: "op_budi", Tags: []string{"management"}}, ErrNotOwnBrokerLogin},
		"shared administrator":        {brokerIdentity{Name: "stratum", Tags: []string{"administrator"}}, ErrNotOwnBrokerLogin},
		"own login but administrator": {brokerIdentity{Name: "op_ana", Tags: []string{"management", "administrator"}}, ErrNotOwnBrokerLogin},
		"default of a DSN with none":  {brokerIdentity{Name: "guest", Tags: []string{"administrator"}}, ErrNotOwnBrokerLogin},
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkOwnBrokerLogin(c.identity, "op_ana"); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestFetchBrokerIdentity(t *testing.T) {
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, _ := r.BasicAuth()
		if r.URL.Path != "/api/whoami" || password != "right" {
			http.Error(w, `{"error":"not_authorized"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"name":"` + user + `","tags":["management"],"is_internal_user":true}`))
	}))
	cfg := mqConfig{apiBase: broker.URL + "/api", user: "op_ana", pass: "right"}

	identity, err := fetchBrokerIdentity(t.Context(), broker.Client(), cfg)
	if err != nil {
		t.Fatalf("own login: %v", err)
	}
	if identity.Name != "op_ana" || len(identity.Tags) != 1 || identity.Tags[0] != "management" {
		t.Errorf("identity = %+v", identity)
	}

	// Neither of these says the login is the wrong person's, so neither may read as
	// a refusal.
	cfg.pass = "wrong"
	if _, err := fetchBrokerIdentity(t.Context(), broker.Client(), cfg); err == nil || errors.Is(err, ErrNotOwnBrokerLogin) {
		t.Errorf("refused login: got %v, want the broker's own error", err)
	}
	broker.Close()
	if _, err := fetchBrokerIdentity(t.Context(), http.DefaultClient, cfg); err == nil || errors.Is(err, ErrNotOwnBrokerLogin) {
		t.Errorf("unreachable: got %v, want the connection error", err)
	}
}
