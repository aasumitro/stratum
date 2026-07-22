package organization

import (
	"context"

	"github.com/aasumitro/stratum/internal/platform/db"
)

func (r *repository) findOrganizationByInviteCode(ctx context.Context, q db.Querier, code string) (*organizationRecord, error) {
	t := new(organizationRecord)
	err := q.QueryRow(ctx, `
		SELECT id, slug, name, status, owner_id, invite_code, invite_code_enabled, timezone, locale, country_code, suspended_at, suspended_reason, settings, created_at, updated_at
		FROM organization.organizations
		WHERE invite_code = $1 AND invite_code_enabled = true AND status = 'active'`,
		code,
	).Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.OwnerID, &t.InviteCode, &t.InviteCodeEnabled, &t.Timezone, &t.Locale, &t.CountryCode, &t.SuspendedAt, &t.SuspendedReason, &t.Settings, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (r *repository) updateInviteCode(ctx context.Context, q db.Querier, organizationID, code string, enabled bool) error {
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations SET invite_code = $2, invite_code_enabled = $3, updated_at = now()
		WHERE id = $1`,
		organizationID, code, enabled,
	)
	return err
}
