package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// ErrJoinAlreadyMember distinguishes an already-a-member code from a
// genuinely invalid one, both at preview (so the join dialog can show the
// state and hide the confirm button instead of letting the user hit it and
// get a confusing "invalid code" on confirm) and at join itself (covers the
// race between the preview check and the insert).
var ErrJoinAlreadyMember = errors.New("already a member")

func (s *service) regenerateInviteCode(ctx context.Context, organizationID string) (code string, err error) {
	defer func() {
		if err != nil {
			code, err = "", apperr.Internal("INVITE_CODE_FAILED", "failed to regenerate invite code", err)
		}
	}()

	code, err = generateToken(8)
	if err != nil {
		return "", fmt.Errorf("organization.regenerateInviteCode: %w", err)
	}
	return code, s.repo.updateInviteCode(ctx, s.pool, organizationID, code, true)
}

func (s *service) toggleInviteCode(ctx context.Context, organizationID string, enabled bool) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Internal("INVITE_CODE_TOGGLE_FAILED", "failed to toggle invite code", err)
		}
	}()

	t, err := s.repo.findOrganizationByID(ctx, s.pool, organizationID)
	if err != nil {
		return fmt.Errorf("organization.toggleInviteCode: %w", err)
	}
	code := ""
	if t.InviteCode != nil {
		code = *t.InviteCode
	}
	if enabled && code == "" {
		code, err = generateToken(8)
		if err != nil {
			return fmt.Errorf("organization.toggleInviteCode: %w", err)
		}
	}
	return s.repo.updateInviteCode(ctx, s.pool, organizationID, code, enabled)
}

// inviteCodePreview is the read-only projection shown before the caller
// commits to joining — organization identity plus who owns it, so the
// join card isn't a leap of faith.
type inviteCodePreview struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	OwnerName        string `json:"owner_name,omitempty"`
	OwnerEmail       string `json:"owner_email,omitempty"`
	OwnerAvatarURL   string `json:"owner_avatar_url,omitempty"`
}

// previewInviteCode looks up the code without joining. A lookup failure
// still maps to the fixed INVITE_CODE_INVALID error — a preview shouldn't
// reveal more about *why* a code failed than the join itself would — but
// the caller already being a member of the resolved organization is
// reported distinctly (ErrJoinAlreadyMember) so the join dialog can show
// that state and hide the confirm button.
func (s *service) previewInviteCode(ctx context.Context, code, authSub string) (inviteCodePreview, error) {
	t, err := s.repo.findOrganizationByInviteCode(ctx, s.pool, code)
	if err != nil {
		return inviteCodePreview{}, apperr.Validation("INVITE_CODE_INVALID", "invalid or disabled invite code")
	}

	preview := inviteCodePreview{OrganizationID: t.ID, OrganizationName: t.Name}
	if _, err := s.repo.getMemberRole(ctx, s.pool, t.ID, authSub); err == nil {
		return preview, ErrJoinAlreadyMember
	}

	if s.userReader != nil {
		if owner, err := s.userReader.GetUserByAuthSub(ctx, t.OwnerID); err == nil && owner != nil {
			preview.OwnerName = owner.Name
			preview.OwnerEmail = owner.Email
			preview.OwnerAvatarURL = owner.AvatarURL
		}
	}
	return preview, nil
}

// joinByCode. A code lookup failure or a unique-violation on the
// membership insert (the already-a-member race between preview and here)
// both fold into the same generic invalid-code error except for
// ErrPlanLimitReached and ErrJoinAlreadyMember, which the handler needs to
// distinguish to render the right message.
func (s *service) joinByCode(ctx context.Context, code, authSub string) (*organizationRecord, error) {
	t, err := s.repo.findOrganizationByInviteCode(ctx, s.pool, code)
	if err != nil {
		return nil, apperr.Validation("INVITE_CODE_INVALID", "invalid or disabled invite code")
	}
	if _, err := s.repo.getMemberRole(ctx, s.pool, t.ID, authSub); err == nil {
		return nil, ErrJoinAlreadyMember
	}

	// Locked (and the limit re-checked against a live count) before the
	// insert below, so two near-simultaneous joins for the same organization
	// can't both pass the check against a stale/equal count and both
	// succeed past the plan's seat limit.
	err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if err := s.repo.lockOrganizationForUpdate(ctx, tx, t.ID); err != nil {
			return err
		}
		if err := s.checkMemberLimitLocked(ctx, tx, t.ID); err != nil {
			return err
		}
		_, err := s.repo.insertMembership(ctx, tx, t.ID, authSub, contracts.RoleMember)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrPlanLimitReached) {
			return nil, ErrPlanLimitReached
		}
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			return nil, ErrJoinAlreadyMember
		}
		return nil, apperr.Validation("INVITE_CODE_INVALID", "invalid or disabled invite code")
	}
	s.syncMemberUsage(ctx, t.ID)
	return t, nil
}
