package organization

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegration_WebhookRepository_KeyVersionDecryption(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	repo := &repository{}

	org, err := repo.insertOrganization(ctx, pool, "test-webhook-org", "Test Org", "owner-1", "ID")
	if err != nil {
		t.Fatalf("failed to create organization: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM organization.organizations WHERE id = $1", org.ID)
	})

	// Insert first row with key_version = 1 (default), encrypted with "first-key"
	ep1, err := repo.insertWebhookEndpoint(ctx, pool, org.ID, "https://example.com/1", "secret1", nil, 1, "first-key", "")
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}

	// Insert second row natively encrypted with "second-key" and key_version = 2 via SQL
	ep2 := new(webhookEndpointRecord)
	err = scanWebhookEndpoint(pool.QueryRow(ctx, `
		INSERT INTO organization.webhook_endpoints (organization_id, url, secret_encrypted, key_version)
		VALUES ($1, $2, pgp_sym_encrypt($3, $4), 2)
		RETURNING `+webhookEndpointSelectColumns(2, 4, 5),
		org.ID, "https://example.com/2", "secret2", "second-key", "first-key",
	), ep2)
	if err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	// List with currentVersion = 2, currentKey = "second-key", previousKey = "first-key"
	recs, err := repo.listWebhookEndpoints(ctx, pool, org.ID, 2, "second-key", "first-key")
	if err != nil {
		t.Fatalf("listWebhookEndpoints: %v", err)
	}

	if len(recs) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(recs))
	}

	found1, found2 := false, false
	for _, r := range recs {
		if r.ID == ep1.ID {
			found1 = true
			if r.SecretPlaintext != "secret1" {
				t.Errorf("ep1: expected secret1, got %q", r.SecretPlaintext)
			}
		}
		if r.ID == ep2.ID {
			found2 = true
			if r.SecretPlaintext != "secret2" {
				t.Errorf("ep2: expected secret2, got %q", r.SecretPlaintext)
			}
		}
	}

	if !found1 {
		t.Errorf("ep1 not found in results")
	}
	if !found2 {
		t.Errorf("ep2 not found in results")
	}

	// Verify insertWebhookEndpoint sets key_version explicitly.
	ep3, err := repo.insertWebhookEndpoint(ctx, pool, org.ID, "https://example.com/3", "secret3", nil, 2, "second-key", "first-key")
	if err != nil {
		t.Fatalf("insert 3: %v", err)
	}
	var kv int
	if err := pool.QueryRow(ctx, "SELECT key_version FROM organization.webhook_endpoints WHERE id = $1", ep3.ID).Scan(&kv); err != nil {
		t.Fatalf("ep3 key_version check: %v", err)
	}
	if kv != 2 {
		t.Errorf("ep3: expected key_version=2, got %d", kv)
	}

	// Verify rotateWebhookSecret re-encrypts previous secret and stamps new key_version.
	ep1Rotated, err := repo.rotateWebhookSecret(ctx, pool, org.ID, ep1.ID, "secret1-new", time.Now().Add(24*time.Hour), 2, "second-key", "first-key")
	if err != nil {
		t.Fatalf("rotate ep1: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT key_version FROM organization.webhook_endpoints WHERE id = $1", ep1.ID).Scan(&kv); err != nil {
		t.Fatalf("ep1 rotated key_version check: %v", err)
	}
	if kv != 2 {
		t.Errorf("ep1 rotated: expected key_version=2, got %d", kv)
	}
	if ep1Rotated.SecretPlaintext != "secret1-new" {
		t.Errorf("ep1 rotated: expected secret1-new, got %q", ep1Rotated.SecretPlaintext)
	}
	if ep1Rotated.SecretPlaintextPrevious == nil || *ep1Rotated.SecretPlaintextPrevious != "secret1" {
		t.Errorf("ep1 rotated: expected previous secret to be 'secret1', got %v", ep1Rotated.SecretPlaintextPrevious)
	}
}
