package organization

import (
	"context"
	"fmt"
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
	if err != nil {
		return "", fmt.Errorf("organization.findFirstOrganizationIDByMember: %w", err)
	}
	return id, nil
}

func (r *repository) insertOwnerMembership(ctx context.Context, q db.Querier, organizationID, authSub string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO organization.memberships (organization_id, auth_sub, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT (organization_id, auth_sub) DO NOTHING`,
		organizationID, authSub,
	)
	if err != nil {
		return fmt.Errorf("organization.insertOwnerMembership: %w", err)
	}
	return nil
}

// removeAllMemberships deletes every membership row for a user across all
// organizations — used by the GDPR account-deletion flow, not by any
// single-organization membership management route.
func (r *repository) removeAllMemberships(ctx context.Context, q db.Querier, authSub string) error {
	if _, err := q.Exec(ctx, `DELETE FROM organization.memberships WHERE auth_sub = $1`, authSub); err != nil {
		return fmt.Errorf("organization.removeAllMemberships: %w", err)
	}
	return nil
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
		return nil, fmt.Errorf("organization.listMembers: %w", err)
	}
	defer rows.Close()

	var out []membershipRecord
	for rows.Next() {
		var m membershipRecord
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.AuthSub, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("organization.listMembers: scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listMembers: %w", err)
	}
	return out, nil
}

func (r *repository) listMemberAuthSubs(ctx context.Context, q db.Querier, organizationID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT auth_sub FROM organization.memberships WHERE organization_id = $1`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.listMemberAuthSubs: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, fmt.Errorf("organization.listMemberAuthSubs: scan: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listMemberAuthSubs: %w", err)
	}
	return out, nil
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
	if err != nil {
		return nil, fmt.Errorf("organization.insertMembership: %w", err)
	}
	return m, nil
}

func (r *repository) deleteMembership(ctx context.Context, q db.Querier, organizationID, authSub string) error {
	_, err := q.Exec(ctx, `
		DELETE FROM organization.memberships WHERE organization_id = $1 AND auth_sub = $2 AND role != 'owner'`,
		organizationID, authSub,
	)
	if err != nil {
		return fmt.Errorf("organization.deleteMembership: %w", err)
	}
	return nil
}

func (r *repository) updateMemberRole(ctx context.Context, q db.Querier, organizationID, authSub, role string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.memberships SET role = $3
		WHERE organization_id = $1 AND auth_sub = $2`,
		organizationID, authSub, role,
	)
	if err != nil {
		return fmt.Errorf("organization.updateMemberRole: %w", err)
	}
	return nil
}

func (r *repository) getMemberRole(ctx context.Context, q db.Querier, organizationID, authSub string) (string, error) {
	var role string
	err := q.QueryRow(ctx, `
		SELECT role FROM organization.memberships
		WHERE organization_id = $1 AND auth_sub = $2`,
		organizationID, authSub,
	).Scan(&role)
	if err != nil {
		return "", fmt.Errorf("organization.getMemberRole: %w", err)
	}
	return role, nil
}

func (r *repository) updateOrganizationOwner(ctx context.Context, q db.Querier, id, newOwnerAuthSub string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations SET owner_id = $2, updated_at = now()
		WHERE id = $1`,
		id, newOwnerAuthSub,
	)
	if err != nil {
		return fmt.Errorf("organization.updateOrganizationOwner: %w", err)
	}
	return nil
}

func (r *repository) countActiveMembers(ctx context.Context, q db.Querier, organizationID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx,
		`SELECT COUNT(*) FROM organization.memberships WHERE organization_id = $1`,
		organizationID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("organization.countActiveMembers: %w", err)
	}
	return n, nil
}

func (r *repository) selectMembersForRemoval(ctx context.Context, q db.Querier, organizationID string, excludeAuthSubs []string, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT auth_sub FROM organization.memberships
		WHERE organization_id = $1 AND role != 'owner' AND NOT (auth_sub = ANY(COALESCE($2, '{}'::text[])))
		ORDER BY joined_at DESC
		LIMIT $3`,
		organizationID, excludeAuthSubs, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.selectMembersForRemoval: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, fmt.Errorf("organization.selectMembersForRemoval: scan: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.selectMembersForRemoval: %w", err)
	}
	return out, nil
}

func (r *repository) bulkRemoveMembers(ctx context.Context, q db.Querier, organizationID string, authSubs []string) ([]string, error) {
	rows, err := q.Query(ctx, `
		DELETE FROM organization.memberships
		WHERE organization_id = $1 AND role != 'owner' AND auth_sub = ANY(COALESCE($2, '{}'::text[]))
		RETURNING auth_sub`,
		organizationID, authSubs,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.bulkRemoveMembers: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, fmt.Errorf("organization.bulkRemoveMembers: scan: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.bulkRemoveMembers: %w", err)
	}
	return out, nil
}

func (r *repository) filterRemovableMembers(ctx context.Context, q db.Querier, organizationID string, authSubs []string) ([]string, error) {
	if len(authSubs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT auth_sub FROM organization.memberships
		WHERE organization_id = $1 AND role != 'owner' AND auth_sub = ANY($2)
		ORDER BY array_position($2::text[], auth_sub)`,
		organizationID, authSubs,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.filterRemovableMembers: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, fmt.Errorf("organization.filterRemovableMembers: scan: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.filterRemovableMembers: %w", err)
	}
	return out, nil
}
