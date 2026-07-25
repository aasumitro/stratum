package organization

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

// ErrPlanLimitReached is shared across member add/invite/join-by-code paths
// that enforce the organization's plan member limit.
var ErrPlanLimitReached = errors.New("plan member limit reached")

// detailKeyOrganizationID is the error-details map key every
// already-a-member response (invitation and invite-code) links back to, so
// the frontend can offer a "go to organization" action.
const detailKeyOrganizationID = "organization_id"

type service struct {
	repo          *repository
	pool          *pgxpool.Pool
	pub           messaging.EventPublisher
	billingReader contracts.BillingReader                // optional; nil = no plan enforcement
	billingWriter contracts.BillingWriter                // optional; nil = usage not recorded
	store         *storage.Client                        // optional; nil = storage disabled
	catalogReader contracts.CatalogReader                // optional; nil = no plan validation on create
	userReader    contracts.UserReader                   // optional; nil = inviter identity omitted from GET /me/invitations
	cacheInval    contracts.OrganizationCacheInvalidator // optional; nil = removed members' cached RBAC role self-expires on its own 30s TTL instead of being invalidated immediately
}

// isUniqueViolation reports whether err is (or wraps) a PostgreSQL
// unique-constraint violation (SQLSTATE 23505) — the raw signal the service
// error-mapping defers turn into a transport-level Conflict.
func isUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505"
}

// generateToken is shared by invitation tokens, invite codes, and webhook
// secrets — all need a random hex string, just at different lengths.
func generateToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b)[:length], nil
}
