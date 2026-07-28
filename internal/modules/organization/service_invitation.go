package organization

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
)

// Invitation-accept sentinels — kept distinguishable end-to-end (unlike
// the previous single generic "invalid or expired" error) so the accept
// UI's five states (valid/expired/already-member/invalid/wrong-account)
// can render the right one instead of one generic error.
var (
	ErrInvitationNotFound      = errors.New("invitation not found")
	ErrInvitationExpired       = errors.New("invitation has expired")
	ErrInvitationEmailMismatch = errors.New("invitation email mismatch")
	ErrInvitationAlreadyMember = errors.New("already a member")
)

const invitationStatusAccepted = "accepted"

// ErrInviteeAlreadyMember means the email being invited already belongs to
// a member of this organization — inviting them again would just create a
// pending invitation nobody needs (accepting it is a no-op, since
// acceptInvitation already treats an accepted-invite-for-an-existing-member
// as idempotent), so it's rejected up front with a clear reason instead of
// silently creating a dead invitation row.
var ErrInviteeAlreadyMember = errors.New("invitee already a member")

// createInvitation deliberately maps every failure (including
// ErrPlanLimitReached and ErrInviteeAlreadyMember) to the same generic 500 —
// matches the pre-migration handler, which never special-cased it here.
// Both handler_invitation.go's createInvitation and importMembers's own
// errors.Is checks on these sentinels still resolve correctly through
// apperr.Unwrap.
func (s *service) createInvitation(
	ctx context.Context, organizationID, email, role, invitedBy string,
) (inv *invitationRecord, err error) {
	defer func() {
		if err != nil {
			inv, err = nil, apperr.Internal("INVITATION_CREATE_FAILED", "failed to create invitation", err)
		}
	}()

	// A membership lookup needs an auth_sub, not an email — resolve one via
	// the account module. No account yet for that email = definitely not a
	// member, so the lookup failing is not itself an error here.
	if s.userReader != nil {
		if u, lookupErr := s.userReader.GetUserByEmail(ctx, email); lookupErr == nil && u != nil {
			if _, memErr := s.repo.getMemberRole(ctx, s.pool, organizationID, u.AuthSub); memErr == nil {
				return nil, ErrInviteeAlreadyMember
			}
		}
	}

	if s.billingReader != nil {
		current, limit, err := s.billingReader.CheckUsageLimit(ctx, organizationID, "members")
		if err == nil && limit >= 0 && current >= int64(limit) {
			return nil, ErrPlanLimitReached
		}
	}

	token, err := generateToken(32)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().AddDate(0, 0, 7)
	inv, err = s.repo.insertInvitation(ctx, s.pool, organizationID, email, role, token, invitedBy, expiresAt)
	if err != nil {
		return nil, err
	}

	events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyMemberInvited, "organization", organizationID,
		events.MemberInvited{OrganizationID: organizationID, Email: email, Role: role, InvitedBy: invitedBy, Token: inv.Token})

	return inv, nil
}

func (s *service) listInvitations(ctx context.Context, organizationID string) ([]invitationRecord, error) {
	invitations, err := s.repo.listInvitations(ctx, s.pool, organizationID)
	if err != nil {
		return nil, apperr.Internal("INVITATIONS_FETCH_FAILED", "failed to list invitations", err)
	}
	return invitations, nil
}

func (s *service) revokeInvitation(ctx context.Context, _, id string) error {
	if err := s.repo.deleteInvitation(ctx, s.pool, id); err != nil {
		return apperr.Internal("INVITATION_REVOKE_FAILED", "failed to revoke invitation", err)
	}
	return nil
}

// acceptInvitationResult carries back just enough detail for the handler to
// build a useful 422 response (which organization to link to on
// already-member, which email the invite actually targeted on mismatch) —
// see the Err* sentinels above.
type acceptInvitationResult struct {
	OrganizationID string
	InvitedEmail   string
}

func (s *service) acceptInvitation(
	ctx context.Context, token, authSub, email string, emailVerified bool,
) (acceptInvitationResult, error) {
	inv, err := s.repo.findInvitationByToken(ctx, s.pool, token)
	if err != nil {
		return acceptInvitationResult{}, ErrInvitationNotFound
	}
	// Any authenticated user could otherwise accept a token issued for a
	// different email — the invitation only ever meant to grant access to
	// the address it was sent to. Fail closed: a caller whose token has no
	// email claim (phone/anonymous/SSO-without-email signups) or an
	// unverified one can't prove they own the invited address, so they're
	// rejected the same as a caller with the wrong email — the random
	// 32-byte token alone isn't treated as sufficient proof.
	if !emailVerified || !strings.EqualFold(inv.Email, email) {
		return acceptInvitationResult{InvitedEmail: inv.Email}, ErrInvitationEmailMismatch
	}
	// Idempotent: already accepted means the user is already a member.
	if inv.Status == invitationStatusAccepted {
		return acceptInvitationResult{OrganizationID: inv.OrganizationID}, ErrInvitationAlreadyMember
	}
	if time.Now().After(inv.ExpiresAt) {
		return acceptInvitationResult{}, ErrInvitationExpired
	}
	if s.billingReader != nil {
		current, limit, err := s.billingReader.CheckUsageLimit(ctx, inv.OrganizationID, "members")
		if err == nil && limit >= 0 && current >= int64(limit) {
			return acceptInvitationResult{}, ErrPlanLimitReached
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return acceptInvitationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// SAVEPOINT guards against a 23505 unique violation aborting the whole
	// transaction — PostgreSQL marks a tx as aborted on any error, so without
	// a savepoint the subsequent acceptInvitation UPDATE would also fail.
	if _, err := tx.Exec(ctx, "SAVEPOINT sp_insert_member"); err != nil {
		return acceptInvitationResult{}, err
	}
	alreadyMember := false
	if _, err := s.repo.insertMembership(ctx, tx, inv.OrganizationID, authSub, inv.Role); err != nil {
		// Unique violation = user is already a member (joined via another path).
		// Still mark the invitation as accepted so it is not dangling.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23505" {
			return acceptInvitationResult{}, err
		}
		// Roll back to savepoint to restore the transaction to a usable state.
		if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT sp_insert_member"); rbErr != nil {
			return acceptInvitationResult{}, rbErr
		}
		alreadyMember = true
	}
	if err := s.repo.acceptInvitation(ctx, tx, inv.ID); err != nil {
		return acceptInvitationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return acceptInvitationResult{}, err
	}
	s.syncMemberUsage(ctx, inv.OrganizationID)
	if alreadyMember {
		return acceptInvitationResult{OrganizationID: inv.OrganizationID}, ErrInvitationAlreadyMember
	}
	return acceptInvitationResult{OrganizationID: inv.OrganizationID}, nil
}

// invitationPreview is the read-only projection shown before the caller
// commits to accepting (the "valid" confirm card — "Join Acme Corp?
// mark@acme.com invited you as Member").
type invitationPreview struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	Role             string `json:"role"`
	InvitedByEmail   string `json:"invited_by_email,omitempty"`
	InvitedByName    string `json:"invited_by_name,omitempty"`
	// InvitedEmail is only populated on an ErrInvitationEmailMismatch
	// return — the address the invitation actually targets.
	InvitedEmail string `json:"invited_email,omitempty"`
}

// previewInvitation runs the same validation acceptInvitation does (found,
// email match, not already accepted, not expired) without writing anything,
// so the accept page can show a proper confirm step instead of accepting
// blind on page load.
func (s *service) previewInvitation(
	ctx context.Context, token, email string, emailVerified bool,
) (invitationPreview, error) {
	inv, err := s.repo.findInvitationByToken(ctx, s.pool, token)
	if err != nil {
		return invitationPreview{}, ErrInvitationNotFound
	}
	// Same fail-closed rule as acceptInvitation — see there for why a
	// missing or unverified email is treated as a mismatch, not skipped.
	if !emailVerified || !strings.EqualFold(inv.Email, email) {
		return invitationPreview{InvitedEmail: inv.Email}, ErrInvitationEmailMismatch
	}
	if inv.Status == invitationStatusAccepted {
		return invitationPreview{OrganizationID: inv.OrganizationID}, ErrInvitationAlreadyMember
	}
	if time.Now().After(inv.ExpiresAt) {
		return invitationPreview{}, ErrInvitationExpired
	}

	preview := invitationPreview{OrganizationID: inv.OrganizationID, Role: inv.Role}
	if ws, err := s.getOrganization(ctx, inv.OrganizationID); err == nil {
		preview.OrganizationName = ws.Name
	}
	if s.userReader != nil {
		if u, err := s.userReader.GetUserByAuthSub(ctx, inv.InvitedBy); err == nil && u != nil {
			preview.InvitedByEmail = u.Email
			preview.InvitedByName = u.Name
		}
	}
	return preview, nil
}

// declineInvitation is the invitee-initiated counterpart to admin revoke —
// same effect (the row is gone, same repo.deleteInvitation call), different
// caller: the invitee themselves, authorized the same way accept/preview
// are (token + verified matching email), not an org role, since a
// pre-membership invitee holds no role to check yet.
func (s *service) declineInvitation(ctx context.Context, token, email string, emailVerified bool) error {
	inv, err := s.repo.findInvitationByToken(ctx, s.pool, token)
	if err != nil {
		return ErrInvitationNotFound
	}
	if !emailVerified || !strings.EqualFold(inv.Email, email) {
		return ErrInvitationEmailMismatch
	}
	if inv.Status == invitationStatusAccepted {
		return ErrInvitationAlreadyMember
	}
	if time.Now().After(inv.ExpiresAt) {
		return ErrInvitationExpired
	}
	if err := s.repo.deleteInvitation(ctx, s.pool, inv.ID); err != nil {
		return err
	}
	events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyInvitationDeclined, "organization", inv.OrganizationID,
		events.InvitationDeclined{OrganizationID: inv.OrganizationID, InvitedBy: inv.InvitedBy, InviteeEmail: inv.Email})
	return nil
}

// listMyInvitations returns the caller's own pending invitations across
// every organization, with the inviter's email resolved when a userReader
// is wired — backs the onboarding auto-surface.
func (s *service) listMyInvitations(ctx context.Context, email string) ([]myInvitationRecord, error) {
	invs, err := s.repo.listInvitationsByEmail(ctx, s.pool, email)
	if err != nil {
		return nil, apperr.Internal("INVITATIONS_FETCH_FAILED", "failed to list invitations", err)
	}
	if s.userReader == nil {
		return invs, nil
	}
	for i := range invs {
		if u, err := s.userReader.GetUserByAuthSub(ctx, invs[i].InvitedBy); err == nil && u != nil {
			invs[i].InvitedByEmail = u.Email
		}
	}
	return invs, nil
}

// requestNewInvitation notifies the ORIGINAL inviter (not the requester)
// that an expired/lost invitation needs resending — the requester has no
// permission to re-invite themselves. Looked up by token regardless of
// status/expiry so this works precisely on the expired-invitation case.
func (s *service) requestNewInvitation(ctx context.Context, token string) error {
	inv, err := s.repo.findInvitationByToken(ctx, s.pool, token)
	if err != nil {
		return apperr.Validation("INVITATION_NOT_FOUND", "invitation not found")
	}
	events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyInvitationRequested, "organization", inv.OrganizationID,
		events.InvitationRequested{OrganizationID: inv.OrganizationID, InvitedBy: inv.InvitedBy, InviteeEmail: inv.Email})
	return nil
}
