package organization

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type invitationRecord struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Email          string    `json:"email"`
	Role           string    `json:"role"`
	Token          string    `json:"-"`
	InvitedBy      string    `json:"invited_by"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
}

func (r *repository) deleteExpiredInvitations(ctx context.Context, q db.Querier) error {
	if _, err := q.Exec(ctx, `DELETE FROM organization.invitations WHERE expires_at < now()`); err != nil {
		return fmt.Errorf("organization.deleteExpiredInvitations: %w", err)
	}
	return nil
}

func (r *repository) insertInvitation(
	ctx context.Context, q db.Querier,
	organizationID, email, role, token, invitedBy string, expiresAt time.Time,
) (*invitationRecord, error) {
	inv := new(invitationRecord)
	// ON CONFLICT refreshes token + expiry so resend works without revoking first.
	err := q.QueryRow(ctx, `
		INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (organization_id, email) DO UPDATE
		  SET token      = EXCLUDED.token,
		      role       = EXCLUDED.role,
		      invited_by = EXCLUDED.invited_by,
		      status     = 'pending',
		      expires_at = EXCLUDED.expires_at
		RETURNING id, organization_id, email, role, token, invited_by, status, expires_at, created_at`,
		organizationID, email, role, token, invitedBy, expiresAt,
	).Scan(&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role, &inv.Token,
		&inv.InvitedBy, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("organization.insertInvitation: %w", err)
	}
	return inv, nil
}

func (r *repository) findInvitationByToken(ctx context.Context, q db.Querier, token string) (*invitationRecord, error) {
	inv := new(invitationRecord)
	err := q.QueryRow(ctx, `
		SELECT id, organization_id, email, role, token, invited_by, status, expires_at, created_at
		FROM organization.invitations
		WHERE token = $1`,
		token,
	).Scan(&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role, &inv.Token,
		&inv.InvitedBy, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("organization.findInvitationByToken: %w", err)
	}
	return inv, nil
}

func (r *repository) acceptInvitation(ctx context.Context, q db.Querier, id string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.invitations SET status = 'accepted' WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("organization.acceptInvitation: %w", err)
	}
	return nil
}

func (r *repository) listInvitations(ctx context.Context, q db.Querier, organizationID string) ([]invitationRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, organization_id, email, role, token, invited_by, status, expires_at, created_at
		FROM organization.invitations
		WHERE organization_id = $1 AND status = 'pending'
		ORDER BY created_at DESC`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.listInvitations: %w", err)
	}
	defer rows.Close()

	var out []invitationRecord
	for rows.Next() {
		var inv invitationRecord
		if err := rows.Scan(&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role, &inv.Token,
			&inv.InvitedBy, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("organization.listInvitations: scan: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listInvitations: %w", err)
	}
	return out, nil
}

func (r *repository) deleteInvitation(ctx context.Context, q db.Querier, id string) error {
	if _, err := q.Exec(ctx, `DELETE FROM organization.invitations WHERE id = $1`, id); err != nil {
		return fmt.Errorf("organization.deleteInvitation: %w", err)
	}
	return nil
}

type myInvitationRecord struct {
	ID               string `json:"id"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	Email            string `json:"email"`
	Role             string `json:"role"`
	InvitedBy        string `json:"invited_by"`
	InvitedByEmail   string `json:"invited_by_email,omitempty"`
	// Token IS exposed here (unlike the admin-facing invitationRecord,
	// which keeps json:"-") so the caller's own onboarding screen can
	// one-click POST /invitations/accept without a redirect — this
	// endpoint is already scoped to the authenticated caller's own email,
	// the same audience the token was already emailed to.
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// listInvitationsByEmail returns pending, non-expired invitations across
// every organization for the given email — backs GET /me/invitations, the
// onboarding "you have a pending invitation" auto-surface. The
// organization join is same-schema so it's done here; InvitedByEmail is
// resolved separately by the service layer via contracts.UserReader, since
// account.users lives in a different schema.
func (r *repository) listInvitationsByEmail(ctx context.Context, q db.Querier, email string) ([]myInvitationRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT i.id, i.organization_id, o.name, i.email, i.role, i.invited_by, i.token, i.expires_at, i.created_at
		FROM organization.invitations i
		JOIN organization.organizations o ON o.id = i.organization_id
		WHERE i.email = $1 AND i.status = 'pending' AND i.expires_at > now()
		ORDER BY i.created_at DESC`,
		email,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.listInvitationsByEmail: %w", err)
	}
	defer rows.Close()

	var out []myInvitationRecord
	for rows.Next() {
		var inv myInvitationRecord
		if err := rows.Scan(&inv.ID, &inv.OrganizationID, &inv.OrganizationName, &inv.Email,
			&inv.Role, &inv.InvitedBy, &inv.Token, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("organization.listInvitationsByEmail: scan: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listInvitationsByEmail: %w", err)
	}
	return out, nil
}
