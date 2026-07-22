package organization

import (
	"context"
	"encoding/json"
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
	return t, err
}

func (r *repository) findOrganizationByID(ctx context.Context, q db.Querier, id string) (*organizationRecord, error) {
	t := new(organizationRecord)
	err := q.QueryRow(ctx, `
		SELECT id, slug, name, status, owner_id, invite_code, invite_code_enabled, timezone, locale, country_code, suspended_at, suspended_reason, settings, created_at, updated_at
		FROM organization.organizations WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.OwnerID, &t.InviteCode, &t.InviteCodeEnabled, &t.Timezone, &t.Locale, &t.CountryCode, &t.SuspendedAt, &t.SuspendedReason, &t.Settings, &t.CreatedAt, &t.UpdatedAt)
	return t, err
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
		return nil, err
	}
	defer rows.Close()

	var out []organizationView
	for rows.Next() {
		var w organizationView
		if err := rows.Scan(
			&w.ID, &w.Slug, &w.Name, &w.Status, &w.OwnerID,
			&w.Role, &w.JoinedAt, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
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
		return nil, err
	}
	defer rows.Close()

	var out []organizationView
	for rows.Next() {
		var w organizationView
		if err := rows.Scan(
			&w.ID, &w.Slug, &w.Name, &w.Status, &w.OwnerID,
			&w.Role, &w.JoinedAt, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
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
	return t, err
}

func (r *repository) softDeleteOrganization(ctx context.Context, q db.Querier, id string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations SET status = 'deleted', updated_at = now() WHERE id = $1`,
		id,
	)
	return err
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
	return count, err
}

// listOwnedOrganizationIDs returns the IDs of every organization a user
// owns, regardless of status — used by billing's trial-eligibility check,
// matching the cross-schema JOIN it replaces (no status filter).
func (r *repository) listOwnedOrganizationIDs(ctx context.Context, q db.Querier, authSub string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM organization.organizations WHERE owner_id = $1`, authSub)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *repository) updateSettings(ctx context.Context, q db.Querier, id, timezone, locale string, allowedIPs []string) error {
	settingsJSON, _ := json.Marshal(map[string]any{settingAllowedIPs: allowedIPs})
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations
		SET timezone = $2, locale = $3,
		    settings = settings || $4::jsonb,
		    updated_at = now()
		WHERE id = $1`,
		id, timezone, locale, string(settingsJSON),
	)
	return err
}

func (r *repository) suspendOrganization(ctx context.Context, q db.Querier, id, reason string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations
		SET status = 'suspended', suspended_at = now(), suspended_reason = $2, updated_at = now()
		WHERE id = $1 AND status = 'active'`,
		id, reason,
	)
	return err
}

func (r *repository) unsuspendOrganization(ctx context.Context, q db.Querier, id string) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations
		SET status = 'active', suspended_at = NULL, suspended_reason = '', updated_at = now()
		WHERE id = $1 AND status = 'suspended'`,
		id,
	)
	return err
}
