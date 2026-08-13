package config_test

import (
	"testing"

	"github.com/aasumitro/stratum/internal/platform/config"
)

func TestRequireSecretsOutsideDev(t *testing.T) {
	base := func() config.Config {
		return config.Config{
			Env:        "production",
			StatsToken: "s",
			Stripe:     config.StripeConfig{WebhookSecret: "s"},
			Xendit:     config.XenditConfig{CallbackToken: "x", AllowedCIDRs: []string{"192.0.2.0/24"}},
			Auth:       config.AuthConfig{WebhookSecret: "a"},
		}
	}

	t.Run("development skips the check entirely", func(t *testing.T) {
		cfg := config.Config{Env: "development"}
		if err := cfg.RequireSecretsOutsideDev(); err != nil {
			t.Errorf("unexpected error in development: %v", err)
		}
	})

	t.Run("production with all secrets set passes", func(t *testing.T) {
		cfg := base()
		if err := cfg.RequireSecretsOutsideDev(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"missing stripe secret", func(c *config.Config) { c.Stripe.WebhookSecret = "" }},
		{"missing xendit token", func(c *config.Config) { c.Xendit.CallbackToken = "" }},
		{"missing xendit cidrs", func(c *config.Config) { c.Xendit.AllowedCIDRs = nil }},
		{"missing supabase secret", func(c *config.Config) { c.Auth.WebhookSecret = "" }},
		{"missing stats token", func(c *config.Config) { c.StatsToken = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			if err := cfg.RequireSecretsOutsideDev(); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestRequireGeoIPDBOutsideDev(t *testing.T) {
	t.Run("development skips the check entirely", func(t *testing.T) {
		cfg := config.Config{Env: "development"}
		if err := cfg.RequireGeoIPDBOutsideDev(); err != nil {
			t.Errorf("unexpected error in development: %v", err)
		}
	})

	t.Run("production with GEOIP_DB_PATH set passes", func(t *testing.T) {
		cfg := config.Config{Env: "production", GeoIPDBPath: "/data/GeoLite2-Country.mmdb"}
		if err := cfg.RequireGeoIPDBOutsideDev(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("production with GEOIP_DB_PATH unset fails", func(t *testing.T) {
		cfg := config.Config{Env: "production"}
		if err := cfg.RequireGeoIPDBOutsideDev(); err == nil {
			t.Error("expected an error, got nil")
		}
	})
}
