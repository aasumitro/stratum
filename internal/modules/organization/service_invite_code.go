package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
	return code, db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		return s.repo.updateInviteCode(ctx, q, organizationID, code, true)
	})
}

func (s *service) toggleInviteCode(ctx context.Context, organizationID string, enabled bool) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Internal("INVITE_CODE_TOGGLE_FAILED", "failed to toggle invite code", err)
		}
	}()

	return db.WithOrgTx(ctx, s.pool, organizationID, func(ctx context.Context, q db.Querier) error {
		t, err := s.repo.findOrganizationByID(ctx, q, organizationID)
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
		return s.repo.updateInviteCode(ctx, q, organizationID, code, enabled)
	})
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

// classifyInviteCodeLookup maps a failed invite-code lookup. No organization
// with that enabled code, or a code Postgres cannot even read (a data
// exception, class 22: invalid UTF-8, a NUL byte), is an invalid code; anything
// else is a failed lookup, logged at the sink rather than told to the user as a
// bad code.
func classifyInviteCodeLookup(err error) error {
	if errors.Is(err, pgx.ErrNoRows) || isDataException(err) {
		return apperr.Validation("INVITE_CODE_INVALID", "invalid or disabled invite code")
	}
	return apperr.Internal("ORGANIZATION_FETCH_FAILED", "failed to get organization", err)
}

// previewInviteCode looks up the code without joining. A code that matches no
// enabled organization is the fixed INVITE_CODE_INVALID error — a preview
// shouldn't reveal more about *why* a code failed than the join itself would —
// and the caller already being a member of the resolved organization is
// reported distinctly (ErrJoinAlreadyMember) so the join dialog can show that
// state and hide the confirm button. A failed lookup is a 500.
// The caller is not a member yet, so this and joinByCode have no
// organization scope to run under: they read and write through the unscoped
// pool, matched by the invite code.
func (s *service) previewInviteCode(ctx context.Context, code, authSub string) (inviteCodePreview, error) {
	t, err := s.repo.findOrganizationByInviteCode(ctx, s.unscoped(), code)
	if err != nil {
		return inviteCodePreview{}, classifyInviteCodeLookup(err)
	}

	preview := inviteCodePreview{OrganizationID: t.ID, OrganizationName: t.Name}
	switch _, err := s.repo.getMemberRole(ctx, s.unscoped(), t.ID, authSub); {
	case err == nil:
		return preview, ErrJoinAlreadyMember
	case !errors.Is(err, pgx.ErrNoRows):
		return inviteCodePreview{}, apperr.Internal("ORGANIZATION_FETCH_FAILED", "failed to get organization", err)
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

// joinByCode joins the caller to the organization the code names. A code that
// matches no enabled organization is INVITE_CODE_INVALID; a unique-violation on
// the membership insert (the already-a-member race between preview and here)
// is ErrJoinAlreadyMember, and a full seat count ErrPlanLimitReached, which the
// handler needs to distinguish to render the right message. Any other failure
// is a 500.
func (s *service) joinByCode(ctx context.Context, code, authSub string) (*organizationRecord, error) {
	t, err := s.repo.findOrganizationByInviteCode(ctx, s.unscoped(), code)
	if err != nil {
		return nil, classifyInviteCodeLookup(err)
	}
	switch _, err := s.repo.getMemberRole(ctx, s.unscoped(), t.ID, authSub); {
	case err == nil:
		return nil, ErrJoinAlreadyMember
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, apperr.Internal("ORGANIZATION_FETCH_FAILED", "failed to get organization", err)
	}

	// Locked (and the limit re-checked against a live count) before the
	// insert below, so two near-simultaneous joins for the same organization
	// can't both pass the check against a stale/equal count and both
	// succeed past the plan's seat limit.
	// checkMemberLimitLocked gets the outer ctx, so billing reads through its
	// own pool rather than this unscoped transaction.
	err = db.WithTx(ctx, s.unscoped(), func(tx db.Querier) error {
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
		return nil, apperr.Internal("ORGANIZATION_JOIN_FAILED", "failed to join the organization", err)
	}
	s.syncMemberUsage(ctx, t.ID)
	return t, nil
}
