package organization

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// listMembers stitches membership rows with profile data (email/full_name/
// avatar_url) resolved in one batch call. A missing userReader (or a lookup
// failure) fails open — members are returned with profile fields omitted
// rather than the whole list erroring out, same nil-safe convention as
// every other optional cross-module dependency in this codebase.
func (s *service) listMembers(ctx context.Context, organizationID string) ([]memberView, error) {
	rows, err := s.repo.listMembers(ctx, s.pool, organizationID)
	if err != nil {
		return nil, apperr.Internal("MEMBERS_FETCH_FAILED", "failed to list members", err)
	}

	var profiles map[string]contracts.UserInfo
	if s.userReader != nil {
		authSubs := make([]string, len(rows))
		for i, r := range rows {
			authSubs[i] = r.AuthSub
		}
		profiles, _ = s.userReader.GetUsersByAuthSubs(ctx, authSubs)
	}

	out := make([]memberView, len(rows))
	for i, r := range rows {
		mv := memberView{ID: r.ID, OrganizationID: r.OrganizationID, AuthSub: r.AuthSub, Role: r.Role, JoinedAt: r.JoinedAt}
		if p, ok := profiles[r.AuthSub]; ok {
			if p.Email != "" {
				mv.Email = &p.Email
			}
			if p.Name != "" {
				mv.FullName = &p.Name
			}
			if p.AvatarURL != "" {
				mv.AvatarURL = &p.AvatarURL
			}
		}
		out[i] = mv
	}
	return out, nil
}

func (s *service) removeAllMemberships(ctx context.Context, authSub string) error {
	return s.repo.removeAllMemberships(ctx, s.pool, authSub)
}

func (s *service) addMember(
	ctx context.Context, organizationID, authSub, role string,
) (rec *membershipRecord, err error) {
	if role != contracts.RoleAdmin && role != contracts.RoleMember {
		return nil, apperr.Validation("INVALID_ROLE", "role must be admin or member")
	}
	if ownerSub, err := s.repo.getOrganizationOwner(ctx, s.pool, organizationID); err == nil && ownerSub == authSub {
		return nil, apperr.Validation("CANNOT_MODIFY_OWNER", "cannot change the owner's role")
	}

	defer func() {
		if err == nil {
			return
		}
		rec = nil
		switch {
		case errors.Is(err, ErrPlanLimitReached):
			err = apperr.Validation("PLAN_LIMIT_REACHED", "member limit reached for your current plan")
		case isUniqueViolation(err):
			err = apperr.Conflict("MEMBER_EXISTS", "member already exists")
		default:
			err = apperr.Internal("MEMBER_ADD_FAILED", "failed to add member", err)
		}
	}()

	err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if lockErr := s.repo.lockOrganizationForUpdate(ctx, tx, organizationID); lockErr != nil {
			return lockErr
		}
		if limitErr := s.checkMemberLimitLocked(ctx, tx, organizationID); limitErr != nil {
			return limitErr
		}
		var insertErr error
		rec, insertErr = s.repo.insertMembership(ctx, tx, organizationID, authSub, role)
		return insertErr
	})
	if err != nil {
		return nil, fmt.Errorf("organization.addMember: %w", err)
	}
	s.syncMemberUsage(ctx, organizationID)
	return rec, nil
}

func (s *service) removeMember(ctx context.Context, organizationID, authSub string) error {
	if ownerSub, err := s.repo.getOrganizationOwner(ctx, s.pool, organizationID); err == nil && ownerSub == authSub {
		return apperr.Forbidden("CANNOT_REMOVE_OWNER", "cannot remove the organization owner")
	}
	var removed bool
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		var err error
		removed, err = s.repo.deleteMembership(ctx, tx, organizationID, authSub)
		if err != nil || !removed {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization, events.RoutingKeyMemberRemoved, "organization", organizationID,
			events.MemberRemoved{OrganizationID: organizationID, AuthSub: authSub})
	})
	if err != nil {
		return apperr.Internal("MEMBER_REMOVE_FAILED", "failed to remove member", err)
	}
	if !removed {
		return apperr.NotFound("MEMBER_NOT_FOUND", "member not found", nil)
	}
	s.syncMemberUsage(ctx, organizationID)
	return nil
}

func (s *service) updateMemberRole(ctx context.Context, organizationID, authSub, role string) error {
	if role != contracts.RoleAdmin && role != contracts.RoleMember {
		return apperr.Validation("INVALID_ROLE", "role must be admin or member")
	}
	if ownerSub, err := s.repo.getOrganizationOwner(ctx, s.pool, organizationID); err == nil && ownerSub == authSub {
		return apperr.Validation("CANNOT_MODIFY_OWNER", "cannot change the owner's role")
	}

	var updated bool
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		var err error
		updated, err = s.repo.updateMemberRole(ctx, tx, organizationID, authSub, role)
		if err != nil || !updated {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization, events.RoutingKeyMemberRoleChanged, "organization", organizationID,
			events.MemberRoleChanged{OrganizationID: organizationID, AuthSub: authSub, Role: role})
	})
	if err != nil {
		return apperr.Internal("MEMBER_ROLE_UPDATE_FAILED", "failed to update member role", err)
	}
	if !updated {
		return apperr.NotFound("MEMBER_NOT_FOUND", "member not found", nil)
	}
	return nil
}

func (s *service) getMemberRole(ctx context.Context, organizationID, authSub string) (string, error) {
	return s.repo.getMemberRole(ctx, s.pool, organizationID, authSub)
}

// checkMemberLimitLocked re-checks the organization's plan member limit
// against a live count from q, not the async billing.usage cache
// CheckUsageLimit normally reads for cheap, non-authoritative checks. Call
// only while holding organizationID's row lock (lockOrganizationForUpdate)
// so two concurrent callers serialize on this check instead of both reading
// "under limit" and both inserting past the plan's seat limit. Fails open
// (no billingReader, a lookup error, or an unlimited plan) the same way
// every other optional billingReader call in this module does.
func (s *service) checkMemberLimitLocked(ctx context.Context, q db.Querier, organizationID string) error {
	if s.billingReader == nil {
		return nil
	}
	_, limit, err := s.billingReader.CheckUsageLimit(ctx, organizationID, "members")
	if err != nil || limit < 0 {
		return nil
	}
	current, err := s.repo.countActiveMembers(ctx, q, organizationID)
	if err != nil {
		return fmt.Errorf("organization.checkMemberLimitLocked: %w", err)
	}
	if current >= int64(limit) {
		return ErrPlanLimitReached
	}
	return nil
}

// syncMemberUsage records the current active-member count for organizationID
// as a fire-and-forget background op. Failures are logged (not silently
// dropped) so usage drift stays visible in the logs — it self-corrects on
// the next mutation, but an operator should still be able to spot a
// persistently failing sync.
func (s *service) syncMemberUsage(ctx context.Context, organizationID string) {
	if s.billingWriter == nil {
		return
	}
	// Clear any transaction stashed in ctx before it crosses into this
	// goroutine — context.WithoutCancel keeps every value on the parent
	// context, and a transaction is only safe for the caller's own
	// goroutine to use. Without this, RecordUsage below (a cross-module
	// call back into billing) can pick up the caller's still-in-flight
	// transaction via QuerierFromContext and use it concurrently with the
	// caller, corrupting pgx's per-connection statement cache.
	ctx = db.WithoutQuerier(context.WithoutCancel(ctx))
	go func() {
		// Bounds the background write so a congested DB connection or a
		// hanging cross-module billing call can't leak this goroutine
		// indefinitely — context.WithoutCancel alone has no deadline.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		count, err := s.repo.countActiveMembers(ctx, s.pool, organizationID)
		if err != nil {
			slog.Error("syncMemberUsage: count active members failed",
				"organization_id", organizationID, "error", err)
			return
		}
		if err := s.billingWriter.RecordUsage(ctx, organizationID, "members", count); err != nil {
			slog.Error("syncMemberUsage: record usage failed",
				"organization_id", organizationID, "count", count, "error", err)
		}
	}()
}

// leaveOrganization and transferOwnership both map every failure to a 422
// carrying the raw err.Error() text — matches the pre-migration handler,
// which passed err.Error() straight through unconditionally for these two.
// Their internal `return err` sites are deliberately NOT wrapped with
// fmt.Errorf("organization.Op: %w", ...) like the rest of this module — a
// wrap's prefix would leak into this user-facing message text via err.Error().
func (s *service) leaveOrganization(ctx context.Context, organizationID, authSub string) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Validation("UNPROCESSABLE", err.Error())
		}
	}()

	role, err := s.repo.getMemberRole(ctx, s.pool, organizationID, authSub)
	if err != nil {
		return err
	}
	if role == contracts.RoleOwner {
		return errors.New("owner cannot leave — transfer ownership first")
	}
	if _, err := s.repo.deleteMembership(ctx, s.pool, organizationID, authSub); err != nil {
		return err
	}
	s.syncMemberUsage(ctx, organizationID)
	return nil
}

func (s *service) transferOwnership(ctx context.Context, organizationID, currentOwner, newOwnerAuthSub string) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Validation("UNPROCESSABLE", err.Error())
		}
	}()

	role, err := s.repo.getMemberRole(ctx, s.pool, organizationID, newOwnerAuthSub)
	if err != nil {
		return errors.New("target user is not a member")
	}
	if role == contracts.RoleOwner {
		return errors.New("target is already the owner")
	}

	return db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if err := s.repo.updateOrganizationOwner(ctx, tx, organizationID, newOwnerAuthSub); err != nil {
			return err
		}
		if _, err := s.repo.updateMemberRole(ctx, tx, organizationID, currentOwner, contracts.RoleAdmin); err != nil {
			return err
		}
		if _, err := s.repo.updateMemberRole(ctx, tx, organizationID, newOwnerAuthSub, contracts.RoleOwner); err != nil {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization, events.RoutingKeyOwnershipTransferred, "organization", organizationID,
			events.OwnershipTransferred{OrganizationID: organizationID, PreviousOwner: currentOwner, NewOwner: newOwnerAuthSub})
	})
}
