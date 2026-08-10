package organization

import (
	"context"
	"fmt"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/db"
)

func (s *service) resolveDowngradeOverage(
	ctx context.Context, organizationID string,
	preferredMemberAuthSubs []string, memberLimit int,
	dryRun bool,
) (contracts.OverageResolution, error) {
	// Initialized to empty, not nil: this struct is marshaled straight into
	// the downgrade endpoint's HTTP response (and the preview's dry-run),
	// and a nil Go slice marshals to JSON null, not [] — a dimension with
	// nothing removed would otherwise come back null, which is exactly the
	// kind of value frontend code reaches for .map() on without expecting to.
	res := contracts.OverageResolution{
		RemovedMemberAuthSubs:  []string{},
		AutoSelectedMemberSubs: []string{},
	}

	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '15s'"); err != nil {
			return fmt.Errorf("organization.ResolveDowngradeOverage: failed to set statement timeout: %w", err)
		}

		toRemove, autoSelected, err := s.resolveMemberOverage(ctx, tx, organizationID, preferredMemberAuthSubs, memberLimit, dryRun)
		if err != nil {
			return err
		}
		if len(toRemove) > 0 {
			res.RemovedMemberAuthSubs = toRemove
			res.AutoSelectedMemberSubs = autoSelected
		}
		return nil
	})

	if err == nil && !dryRun {
		s.publishMemberRemovalSideEffects(ctx, organizationID, res.RemovedMemberAuthSubs)
	}

	return res, err
}

// resolveMemberOverage counts organizationID's active members against memberLimit
// and, if over, picks which members to remove — the owner's preferredMemberAuthSubs
// first (in caller order, capped at the overage), then auto-selecting to fill any
// remainder — and (unless dryRun) deletes them. Must run inside tx, the same
// transaction resolveDowngradeOverage uses for its other work. memberLimit < 0 means
// unlimited: returns immediately with nothing to remove.
func (s *service) resolveMemberOverage(
	ctx context.Context, tx db.Querier, organizationID string,
	preferredMemberAuthSubs []string, memberLimit int, dryRun bool,
) (toRemove, autoSelected []string, err error) {
	if memberLimit < 0 {
		return nil, nil, nil
	}

	currentMembers, err := s.repo.countActiveMembers(ctx, tx, organizationID)
	if err != nil {
		return nil, nil, fmt.Errorf("organization.resolveMemberOverage: count members: %w", err)
	}

	overage := int(currentMembers) - memberLimit
	if overage <= 0 {
		return nil, nil, nil
	}

	var validPreferred []string
	if len(preferredMemberAuthSubs) > 0 {
		validPreferred, err = s.repo.filterRemovableMembers(ctx, tx, organizationID, preferredMemberAuthSubs)
		if err != nil {
			return nil, nil, fmt.Errorf("organization.resolveMemberOverage: filter preferred members: %w", err)
		}
	}

	if len(validPreferred) > overage {
		validPreferred = validPreferred[:overage]
	}

	needMore := overage - len(validPreferred)
	if needMore > 0 {
		autoSelected, err = s.repo.selectMembersForRemoval(ctx, tx, organizationID, validPreferred, needMore)
		if err != nil {
			return nil, nil, fmt.Errorf("organization.resolveMemberOverage: select members for removal: %w", err)
		}
	}

	toRemove = make([]string, 0, len(validPreferred)+len(autoSelected))
	toRemove = append(toRemove, validPreferred...)
	toRemove = append(toRemove, autoSelected...)
	if len(toRemove) > 0 && !dryRun {
		if _, err = s.repo.bulkRemoveMembers(ctx, tx, organizationID, toRemove); err != nil {
			return nil, nil, fmt.Errorf("organization.resolveMemberOverage: remove members: %w", err)
		}
	}

	return toRemove, autoSelected, nil
}

// publishMemberRemovalSideEffects runs the post-commit work for a downgrade's bulk
// member removal: usage-cache resync, one event per removed member (published only
// after the transaction has actually committed — matches removeMember's
// single-member path in service_member.go, so a bulk downgrade removal notifies its
// members the same way an individual removal already does), and per-member role-cache
// invalidation (also matching removeMember's handler_member.go invalidateRole call:
// without it a bulk-removed member keeps their cached RBAC role, and with it access
// to the organization, until the cache's own TTL expires on its own). No-op if
// nothing was actually removed.
func (s *service) publishMemberRemovalSideEffects(ctx context.Context, organizationID string, removedAuthSubs []string) {
	if len(removedAuthSubs) == 0 {
		return
	}
	s.syncMemberUsage(ctx, organizationID)
	for _, authSub := range removedAuthSubs {
		events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyMemberRemoved, "organization", organizationID,
			events.MemberRemoved{OrganizationID: organizationID, AuthSub: authSub})
		if s.cacheInval != nil {
			s.cacheInval.InvalidateMemberRole(ctx, organizationID, authSub)
		}
	}
}
