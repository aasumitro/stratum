package billing

import (
	"context"

	"github.com/aasumitro/stratum/internal/platform/apperr"
)

func (s *service) attachAddon(ctx context.Context, organizationID, addonID string, quantity int) error {
	if _, err := s.addonCatalog(ctx, addonID); err != nil {
		return apperr.NotFound("ADDON_NOT_FOUND", "addon not found", ErrAddonNotFound)
	}
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return apperr.Internal("ADDON_ATTACH_FAILED", "failed to attach addon", err)
	}
	if err := s.repo.upsertSubscriptionAddon(ctx, s.querier(ctx), sub.ID, addonID, quantity); err != nil {
		return apperr.Internal("ADDON_ATTACH_FAILED", "failed to attach addon", err)
	}
	return nil
}

func (s *service) detachAddon(ctx context.Context, organizationID, addonID string) error {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return apperr.Internal("ADDON_DETACH_FAILED", "failed to detach addon", err)
	}
	if err := s.repo.deleteSubscriptionAddon(ctx, s.querier(ctx), sub.ID, addonID); err != nil {
		return apperr.Internal("ADDON_DETACH_FAILED", "failed to detach addon", err)
	}
	return nil
}

func (s *service) listAddons(ctx context.Context, organizationID string) ([]attachedAddonRecord, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return nil, apperr.Internal("ADDONS_FETCH_FAILED", "failed to list addons", err)
	}
	addons, err := s.repo.listAttachedAddonsWithPricing(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return nil, apperr.Internal("ADDONS_FETCH_FAILED", "failed to list addons", err)
	}
	return addons, nil
}
