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

		// 1. Members
		if memberLimit >= 0 {
			currentMembers, err := s.repo.countActiveMembers(ctx, tx, organizationID)
			if err != nil {
				return fmt.Errorf("organization.resolveDowngradeOverage: count members: %w", err)
			}

			overage := int(currentMembers) - memberLimit
			if overage > 0 {
				var validPreferred []string
				if len(preferredMemberAuthSubs) > 0 {
					validPreferred, err = s.repo.filterRemovableMembers(ctx, tx, organizationID, preferredMemberAuthSubs)
					if err != nil {
						return fmt.Errorf("organization.resolveDowngradeOverage: filter preferred members: %w", err)
					}
				}

				if len(validPreferred) > overage {
					validPreferred = validPreferred[:overage]
				}

				needMore := overage - len(validPreferred)
				var autoSelected []string
				if needMore > 0 {
					autoSelected, err = s.repo.selectMembersForRemoval(ctx, tx, organizationID, validPreferred, needMore)
					if err != nil {
						return fmt.Errorf("organization.resolveDowngradeOverage: select members for removal: %w", err)
					}
				}

				toRemove := make([]string, 0, len(validPreferred)+len(autoSelected))
				toRemove = append(toRemove, validPreferred...)
				toRemove = append(toRemove, autoSelected...)
				if len(toRemove) > 0 {
					if !dryRun {
						_, err = s.repo.bulkRemoveMembers(ctx, tx, organizationID, toRemove)
						if err != nil {
							return fmt.Errorf("organization.resolveDowngradeOverage: remove members: %w", err)
						}
					}
					res.RemovedMemberAuthSubs = toRemove
					res.AutoSelectedMemberSubs = autoSelected
				}
			}
		}

		return nil
	})

	if err == nil && !dryRun {
		if len(res.RemovedMemberAuthSubs) > 0 {
			s.syncMemberUsage(ctx, organizationID)
			// One event per removed member, published only after the
			// transaction above has actually committed — matches
			// removeMember's single-member path (service_member.go) so a
			// bulk downgrade removal notifies its members the same way an
			// individual removal already does.
			for _, authSub := range res.RemovedMemberAuthSubs {
				events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyMemberRemoved, "organization", organizationID,
					events.MemberRemoved{OrganizationID: organizationID, AuthSub: authSub})
				// Also matches removeMember's single-member path
				// (handler_member.go's h.invalidateRole): without this, a
				// bulk-removed member keeps their cached RBAC role — and
				// with it, access to the organization — until the cache's
				// own TTL expires on its own, rather than losing it the
				// moment the removal actually happens.
				if s.cacheInval != nil {
					s.cacheInval.InvalidateMemberRole(ctx, organizationID, authSub)
				}
			}
		}
	}

	return res, err
}
