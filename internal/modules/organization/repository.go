package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type repository struct{}

type organizationRecord struct {
	ID                string          `json:"id"`
	Slug              string          `json:"slug"`
	Name              string          `json:"name"`
	Status            string          `json:"status"`
	OwnerID           string          `json:"owner_id"`
	InviteCode        *string         `json:"invite_code,omitempty"`
	InviteCodeEnabled bool            `json:"invite_code_enabled"`
	Timezone          string          `json:"timezone"`
	Locale            string          `json:"locale"`
	CountryCode       string          `json:"country_code"`
	SuspendedAt       *time.Time      `json:"suspended_at,omitempty"`
	SuspendedReason   string          `json:"suspended_reason,omitempty"`
	Settings          json.RawMessage `json:"settings,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// organizationView is an organization row enriched with the caller's membership role.
type organizationView struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	OwnerID   string    `json:"owner_id"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joined_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *repository) insertOrganization(
	ctx context.Context, q db.Querier,
	slug, name, ownerID, countryCode string,
) (*organizationRecord, error) {
	t := new(organizationRecord)
	err := q.QueryRow(ctx, `
		INSERT INTO organization.organizations (slug, name, owner_id, country_code)
		VALUES ($1, $2, $3, $4)
		RETURNING id, slug, name, status, owner_id, invite_code, invite_code_enabled, timezone, locale, country_code, suspended_at, suspended_reason, settings, created_at, updated_at`,
		slug, name, ownerID, countryCode,
	).Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.OwnerID, &t.InviteCode, &t.InviteCodeEnabled, &t.Timezone, &t.Locale, &t.CountryCode, &t.SuspendedAt, &t.SuspendedReason, &t.Settings, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("organization.insertOrganization: %w", err)
	}
	return t, nil
}

func (r *repository) findOrganizationByID(ctx context.Context, q db.Querier, id string) (*organizationRecord, error) {
	t := new(organizationRecord)
	err := q.QueryRow(ctx, `
		SELECT id, slug, name, status, owner_id, invite_code, invite_code_enabled, timezone, locale, country_code, suspended_at, suspended_reason, settings, created_at, updated_at
		FROM organization.organizations WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.OwnerID, &t.InviteCode, &t.InviteCodeEnabled, &t.Timezone, &t.Locale, &t.CountryCode, &t.SuspendedAt, &t.SuspendedReason, &t.Settings, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("organization.findOrganizationByID: %w", err)
	}
	return t, nil
}

func (r *repository) findOrganizationsByMember(ctx context.Context, q db.Querier, authSub string) ([]organizationView, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, t.slug, t.name, t.status, t.owner_id, m.role, m.joined_at, t.created_at, t.updated_at
		FROM organization.organizations t
		JOIN organization.memberships m ON m.organization_id = t.id
		WHERE m.auth_sub = $1 AND t.status != 'deleted'
		ORDER BY m.joined_at`,
		authSub,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.findOrganizationsByMember: %w", err)
	}
	defer rows.Close()

	var out []organizationView
	for rows.Next() {
		var w organizationView
		if err := rows.Scan(
			&w.ID, &w.Slug, &w.Name, &w.Status, &w.OwnerID,
			&w.Role, &w.JoinedAt, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("organization.findOrganizationsByMember: scan: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.findOrganizationsByMember: %w", err)
	}
	return out, nil
}

// listMembershipsForExport is findOrganizationsByMember without the
// status != 'deleted' filter — the GDPR data-export flow must not silently
// drop membership history for an organization that was later deleted.
func (r *repository) listMembershipsForExport(ctx context.Context, q db.Querier, authSub string) ([]organizationView, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, t.slug, t.name, t.status, t.owner_id, m.role, m.joined_at, t.created_at, t.updated_at
		FROM organization.organizations t
		JOIN organization.memberships m ON m.organization_id = t.id
		WHERE m.auth_sub = $1
		ORDER BY m.joined_at`,
		authSub,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.listMembershipsForExport: %w", err)
	}
	defer rows.Close()

	var out []organizationView
	for rows.Next() {
		var w organizationView
		if err := rows.Scan(
			&w.ID, &w.Slug, &w.Name, &w.Status, &w.OwnerID,
			&w.Role, &w.JoinedAt, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("organization.listMembershipsForExport: scan: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listMembershipsForExport: %w", err)
	}
	return out, nil
}

func (r *repository) updateOrganization(
	ctx context.Context, q db.Querier,
	id, name, slug string,
) (*organizationRecord, error) {
	t := new(organizationRecord)
	err := q.QueryRow(ctx, `
		UPDATE organization.organizations
		SET name       = COALESCE(NULLIF($2, ''), name),
		    slug       = COALESCE(NULLIF($3, ''), slug),
		    updated_at = now()
		WHERE id = $1 AND status != 'deleted'
		RETURNING id, slug, name, status, owner_id, invite_code, invite_code_enabled, timezone, locale, country_code, suspended_at, suspended_reason, settings, created_at, updated_at`,
		id, name, slug,
	).Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.OwnerID, &t.InviteCode, &t.InviteCodeEnabled, &t.Timezone, &t.Locale, &t.CountryCode, &t.SuspendedAt, &t.SuspendedReason, &t.Settings, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("organization.updateOrganization: %w", err)
	}
	return t, nil
}

func (r *repository) softDeleteOrganization(ctx context.Context, q db.Querier, id string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations SET status = 'deleted', updated_at = now() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("organization.softDeleteOrganization: %w", err)
	}
	return nil
}

// countActiveOwnedOrganizations counts non-deleted organizations a user
// owns — used by account's GDPR delete-account gate (excludes deleted
// organizations, unlike listOwnedOrganizationIDs).
func (r *repository) countActiveOwnedOrganizations(ctx context.Context, q db.Querier, authSub string) (int, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM organization.organizations
		WHERE owner_id = $1 AND status != 'deleted'`,
		authSub,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("organization.countActiveOwnedOrganizations: %w", err)
	}
	return count, nil
}

// listOwnedOrganizationIDs returns the IDs of every organization a user
// owns, regardless of status — used by billing's trial-eligibility check.
func (r *repository) listOwnedOrganizationIDs(ctx context.Context, q db.Querier, authSub string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM organization.organizations WHERE owner_id = $1`, authSub)
	if err != nil {
		return nil, fmt.Errorf("organization.listOwnedOrganizationIDs: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("organization.listOwnedOrganizationIDs: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listOwnedOrganizationIDs: %w", err)
	}
	return out, nil
}

func (r *repository) getOrganizationOwner(ctx context.Context, q db.Querier, id string) (string, error) {
	var ownerID string
	if err := q.QueryRow(ctx, `SELECT owner_id FROM organization.organizations WHERE id = $1`, id).Scan(&ownerID); err != nil {
		return "", fmt.Errorf("organization.getOrganizationOwner: %w", err)
	}
	return ownerID, nil
}

func (r *repository) getOrganizationStatus(ctx context.Context, q db.Querier, id string) (string, error) {
	var status string
	if err := q.QueryRow(ctx, `SELECT status FROM organization.organizations WHERE id = $1`, id).Scan(&status); err != nil {
		return "", fmt.Errorf("organization.getOrganizationStatus: %w", err)
	}
	return status, nil
}

// lockOrganizationForUpdate takes a row lock on the organization for the
// rest of the caller's transaction. Used by addMember/acceptInvitation/
// joinByCode so two concurrent member-adds for the same organization
// serialize on the member-limit check-then-insert instead of both reading
// "under limit" and both inserting past the plan's seat limit.
func (r *repository) lockOrganizationForUpdate(ctx context.Context, q db.Querier, id string) error {
	var got string
	if err := q.QueryRow(ctx, `SELECT id FROM organization.organizations WHERE id = $1 FOR UPDATE`, id).Scan(&got); err != nil {
		return fmt.Errorf("organization.lockOrganizationForUpdate: %w", err)
	}
	return nil
}

func (r *repository) updateSettings(ctx context.Context, q db.Querier, id, timezone, locale string, allowedIPs *[]string) error {
	// Only include allowed_ips in the patch when the caller actually sent
	// it — jsonb's `||` operator leaves keys absent from the right-hand
	// operand untouched, so an omitted field never overwrites the existing
	// allowlist.
	patch := map[string]any{}
	if allowedIPs != nil {
		patch[settingAllowedIPs] = *allowedIPs
	}
	settingsJSON, _ := json.Marshal(patch)
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations
		SET timezone = $2, locale = $3,
		    settings = settings || $4::jsonb,
		    updated_at = now()
		WHERE id = $1`,
		id, timezone, locale, string(settingsJSON),
	)
	if err != nil {
		return fmt.Errorf("organization.updateSettings: %w", err)
	}
	return nil
}

func (r *repository) suspendOrganization(ctx context.Context, q db.Querier, id, reason string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations
		SET status = 'suspended', suspended_at = now(), suspended_reason = $2, updated_at = now()
		WHERE id = $1 AND status = 'active'`,
		id, reason,
	)
	if err != nil {
		return fmt.Errorf("organization.suspendOrganization: %w", err)
	}
	return nil
}

func (r *repository) unsuspendOrganization(ctx context.Context, q db.Querier, id string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations
		SET status = 'active', suspended_at = NULL, suspended_reason = '', updated_at = now()
		WHERE id = $1 AND status = 'suspended'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("organization.unsuspendOrganization: %w", err)
	}
	return nil
}

func (r *repository) updateLogoURL(ctx context.Context, q db.Querier, organizationID, logoURL string) error {
	logoJSON, _ := json.Marshal(map[string]string{"logo_url": logoURL})
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations SET settings = settings || $2::jsonb, updated_at = now() WHERE id = $1`,
		organizationID, string(logoJSON),
	)
	if err != nil {
		return fmt.Errorf("organization.updateLogoURL: %w", err)
	}
	return nil
}
