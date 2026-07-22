package organization

import (
	"context"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type membershipRecord struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	AuthSub        string    `json:"auth_sub"`
	Role           string    `json:"role"`
	JoinedAt       time.Time `json:"joined_at"`
}

// memberView enriches a membership row with profile data from account.users.
type memberView struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	AuthSub        string    `json:"auth_sub"`
	Role           string    `json:"role"`
	JoinedAt       time.Time `json:"joined_at"`
	Email          *string   `json:"email,omitempty"`
	FullName       *string   `json:"full_name,omitempty"`
	AvatarURL      *string   `json:"avatar_url,omitempty"`
}

// findFirstOrganizationIDByMember returns the earliest-joined organization for
// authSub, or "" (pgx.ErrNoRows) if they belong to none. Used to anchor personal
// notifications that have no organization context of their own.
func (r *repository) findFirstOrganizationIDByMember(ctx context.Context, q db.Querier, authSub string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `
		SELECT t.id
		FROM organization.organizations t
		JOIN organization.memberships m ON m.organization_id = t.id
		WHERE m.auth_sub = $1 AND t.status != 'deleted'
		ORDER BY m.joined_at
		LIMIT 1`,
		authSub,
	).Scan(&id)
	return id, err
}

func (r *repository) insertOwnerMembership(ctx context.Context, q db.Querier, organizationID, authSub string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO organization.memberships (organization_id, auth_sub, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT (organization_id, auth_sub) DO NOTHING`,
		organizationID, authSub,
	)
	return err
}

// removeAllMemberships deletes every membership row for a user across all
// organizations — used by the GDPR account-deletion flow, not by any
// single-organization membership management route.
func (r *repository) removeAllMemberships(ctx context.Context, q db.Querier, authSub string) error {
	_, err := q.Exec(ctx, `DELETE FROM organization.memberships WHERE auth_sub = $1`, authSub)
	return err
}

func (r *repository) listMembers(ctx context.Context, q db.Querier, organizationID string) ([]membershipRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, organization_id, auth_sub, role, joined_at
		FROM organization.memberships
		WHERE organization_id = $1
		ORDER BY joined_at`,
		organizationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []membershipRecord
	for rows.Next() {
		var m membershipRecord
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.AuthSub, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *repository) listMemberAuthSubs(ctx context.Context, q db.Querier, organizationID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT auth_sub FROM organization.memberships WHERE organization_id = $1`,
		organizationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (r *repository) insertMembership(
	ctx context.Context, q db.Querier,
	organizationID, authSub, role string,
) (*membershipRecord, error) {
	m := new(membershipRecord)
	err := q.QueryRow(ctx, `
		INSERT INTO organization.memberships (organization_id, auth_sub, role)
		VALUES ($1, $2, $3)
		RETURNING id, organization_id, auth_sub, role, joined_at`,
		organizationID, authSub, role,
	).Scan(&m.ID, &m.OrganizationID, &m.AuthSub, &m.Role, &m.JoinedAt)
	return m, err
}

func (r *repository) deleteMembership(ctx context.Context, q db.Querier, organizationID, authSub string) error {
	_, err := q.Exec(ctx, `
		DELETE FROM organization.memberships WHERE organization_id = $1 AND auth_sub = $2`,
		organizationID, authSub,
	)
	return err
}

func (r *repository) updateMemberRole(ctx context.Context, q db.Querier, organizationID, authSub, role string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.memberships SET role = $3
		WHERE organization_id = $1 AND auth_sub = $2`,
		organizationID, authSub, role,
	)
	return err
}

func (r *repository) getMemberRole(ctx context.Context, q db.Querier, organizationID, authSub string) (string, error) {
	var role string
	err := q.QueryRow(ctx, `
		SELECT role FROM organization.memberships
		WHERE organization_id = $1 AND auth_sub = $2`,
		organizationID, authSub,
	).Scan(&role)
	return role, err
}

func (r *repository) updateOrganizationOwner(ctx context.Context, q db.Querier, id, newOwnerAuthSub string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations SET owner_id = $2, updated_at = now()
		WHERE id = $1`,
		id, newOwnerAuthSub,
	)
	return err
}

func (r *repository) countActiveMembers(ctx context.Context, q db.Querier, organizationID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx,
		`SELECT COUNT(*) FROM organization.memberships WHERE organization_id = $1`,
		organizationID,
	).Scan(&n)
	return n, err
}
