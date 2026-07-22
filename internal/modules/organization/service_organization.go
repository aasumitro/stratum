package organization

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/logger"
)

var ErrUnknownPlan = errors.New("unknown plan")

// ErrUnknownAddon and ErrInvalidCoupon mirror ErrUnknownPlan's reasoning:
// reject before the organization is written, so a bad selection never leaves
// behind an org whose subscription-provisioning event fails forever.
var (
	ErrUnknownAddon  = errors.New("unknown addon")
	ErrInvalidCoupon = errors.New("coupon not valid for this account")
)

// addonSelection is one addon + quantity chosen at organization-creation
// time — the organization module's own copy of events.AddonSelection's
// shape, kept separate so this package never imports billing's types
// directly (only via internal/contracts, per the no-cross-module-import rule).
type addonSelection struct {
	AddonID  string
	Quantity int
}

// ErrIPAllowlistLocksOutCaller: saving this CIDR list would reject
// the caller's own current IP on the very next request.
var ErrIPAllowlistLocksOutCaller = errors.New("this allowlist would lock out your own IP address")

// ErrCannotSelfUnsuspendBillingHold — the owner-facing self-service
// unsuspend (the Danger zone card) must not double as a free escape
// hatch from a billing-driven suspension (unpaid invoice). Only a
// self-triggered suspend (suspendReasonSelfService) is self-reversible;
// anything else — currently only billing's "subscription expired" —
// requires paying the open invoice instead, same as before this route existed.
var ErrCannotSelfUnsuspendBillingHold = errors.New("this organization was suspended for an unpaid invoice — pay it to reactivate")

// createOrganization creates an organization + owner membership atomically,
// then publishes OrganizationCreated so billing can provision a
// subscription for the chosen plan and cycle — both required by the caller
// (organization.createOrganizationRequest), never defaulted here or
// downstream. Addons and couponCode are optional "cart" additions billing
// attaches to the new subscription before composing its first invoice. When
// a catalogReader is wired, plan, every addon ID, and a non-empty coupon
// code are all validated against the live catalog before any DB write
// happens, so a bad selection never leaves a half-created organization (or
// one whose subscription-provisioning event fails forever) behind.
func (s *service) createOrganization(
	ctx context.Context, slug, name, ownerID, countryCode, plan, cycle string,
	addons []addonSelection, couponCode string,
) (org *organizationRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		org = nil
		switch {
		case errors.Is(err, ErrUnknownPlan):
			err = apperr.Validation("UNKNOWN_PLAN", "unknown plan")
		case errors.Is(err, ErrUnknownAddon):
			err = apperr.Validation("UNKNOWN_ADDON", "unknown addon")
		case errors.Is(err, ErrInvalidCoupon):
			err = apperr.Validation("INVALID_COUPON", "coupon not valid for this account")
		case isUniqueViolation(err):
			err = apperr.Conflict("SLUG_TAKEN", "slug already taken")
		default:
			logger.FromContext(ctx).Error("createOrganization failed", "slug", slug, "owner", ownerID, "error", err)
			err = apperr.Internal("ORGANIZATION_CREATE_FAILED", "failed to create organization", err)
		}
	}()

	if s.catalogReader != nil {
		if _, err := s.catalogReader.GetPlanByID(ctx, plan); err != nil {
			return nil, ErrUnknownPlan
		}
		for _, a := range addons {
			if _, err := s.catalogReader.GetAddonByID(ctx, a.AddonID); err != nil {
				return nil, ErrUnknownAddon
			}
		}
		if couponCode != "" {
			if err := s.catalogReader.ValidateCouponCode(ctx, couponCode, ownerID); err != nil {
				return nil, ErrInvalidCoupon
			}
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	t, err := s.repo.insertOrganization(ctx, tx, slug, name, ownerID, countryCode)
	if err != nil {
		return nil, err
	}
	if err := s.repo.insertOwnerMembership(ctx, tx, t.ID, ownerID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	eventAddons := make([]events.AddonSelection, len(addons))
	for i, a := range addons {
		eventAddons[i] = events.AddonSelection{AddonID: a.AddonID, Quantity: a.Quantity}
	}

	events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyOrganizationCreated, "organization", t.ID,
		events.OrganizationCreated{
			OrganizationID: t.ID, Slug: t.Slug, Name: t.Name, CreatedBy: t.OwnerID,
			CountryCode: t.CountryCode, Plan: plan, Cycle: cycle,
			Addons: eventAddons, CouponCode: couponCode, CreatedAt: t.CreatedAt,
		})
	return t, nil
}

func (s *service) listOrganizations(ctx context.Context, authSub string) ([]organizationView, error) {
	return s.repo.findOrganizationsByMember(ctx, s.pool, authSub)
}

func (s *service) listMembershipsForExport(ctx context.Context, authSub string) ([]organizationView, error) {
	return s.repo.listMembershipsForExport(ctx, s.pool, authSub)
}

func (s *service) listOwnedOrganizationIDs(ctx context.Context, authSub string) ([]string, error) {
	return s.repo.listOwnedOrganizationIDs(ctx, s.pool, authSub)
}

func (s *service) countActiveOwnedOrganizations(ctx context.Context, authSub string) (int, error) {
	return s.repo.countActiveOwnedOrganizations(ctx, s.pool, authSub)
}

// getOrganization is also exposed cross-module as contracts.OrganizationReader's
// GetOrganizationByID — every non-handler caller only checks err == nil, so
// classifying here is safe (errors.Is against the wrapped cause still works
// via apperr.Error.Unwrap).
func (s *service) getOrganization(ctx context.Context, id string) (*organizationRecord, error) {
	t, err := s.repo.findOrganizationByID(ctx, s.pool, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("ORGANIZATION_NOT_FOUND", "organization not found", err)
		}
		return nil, apperr.Internal("ORGANIZATION_FETCH_FAILED", "failed to get organization", err)
	}
	return t, nil
}

func (s *service) updateOrganization(ctx context.Context, id, name, slug string) (*organizationRecord, error) {
	t, err := s.repo.updateOrganization(ctx, s.pool, id, name, slug)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("SLUG_TAKEN", "slug already taken")
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("ORGANIZATION_NOT_FOUND", "organization not found", err)
		}
		return nil, apperr.Internal("ORGANIZATION_UPDATE_FAILED", "failed to update organization", err)
	}
	return t, nil
}

func (s *service) deleteOrganization(ctx context.Context, id string) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Internal("ORGANIZATION_DELETE_FAILED", "failed to delete organization", err)
		}
	}()

	if err := s.repo.softDeleteOrganization(ctx, s.pool, id); err != nil {
		return err
	}

	// Best-effort storage cleanup — wipe logo and all uploaded files.
	// Errors are non-fatal: organization is already marked deleted in DB.
	if s.store != nil {
		_ = s.store.Delete(ctx, "organization", id+"/logo")

		if paths, err := s.repo.deleteAllFilesForOrganization(ctx, s.pool, id); err == nil {
			for _, p := range paths {
				_ = s.store.Delete(ctx, "organization-files", p)
			}
		}
	}

	events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyOrganizationDeleted, "organization", id,
		events.OrganizationDeleted{OrganizationID: id, DeletedAt: time.Now()})
	return nil
}

func (s *service) updateSettings(
	ctx context.Context,
	organizationID, timezone, locale, callerIP string,
	allowedIPs []string,
) error {
	if ipLocksOutCaller(callerIP, allowedIPs) {
		return apperr.Validation("IP_ALLOWLIST_LOCKS_OUT_CALLER", ErrIPAllowlistLocksOutCaller.Error())
	}
	if err := s.repo.updateSettings(ctx, s.pool, organizationID, timezone, locale, allowedIPs); err != nil {
		return apperr.Internal("SETTINGS_UPDATE_FAILED", "failed to update settings", err)
	}
	return nil
}

// ipLocksOutCaller reports whether allowedIPs (the CIDR allowlist) would
// reject callerIP — an empty list means "allow all", so it never locks
// anyone out. callerIP that fails to parse (shouldn't happen — Gin's
// ClientIP() always returns a valid address) is treated as locked-out, the
// safe default: reject the save rather than risk silently letting an
// unparseable IP through a CIDR check.
func ipLocksOutCaller(callerIP string, allowedIPs []string) bool {
	if len(allowedIPs) == 0 {
		return false
	}
	ip := net.ParseIP(callerIP)
	if ip == nil {
		return true
	}
	for _, cidr := range allowedIPs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			// A bare IP (no /suffix) is also a valid allowlist entry.
			if entryIP := net.ParseIP(cidr); entryIP != nil && entryIP.Equal(ip) {
				return false
			}
			continue
		}
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

// suspendReasonSelfService marks a suspension as owner-self-triggered
// (vs. billing's "subscription expired" or a Studio operator's own free-text
// reason) — the one reason string selfUnsuspendOrganization will reverse
// without requiring anything else first.
const suspendReasonSelfService = "Suspended by organization owner"

// suspendOrganization is exposed cross-module as contracts.OrganizationSuspender
// (billing's auto-suspend-on-expiry, error discarded there) as well as the
// owner-facing HTTP route — safe to classify unconditionally.
func (s *service) suspendOrganization(ctx context.Context, organizationID, reason string) error {
	if err := s.repo.suspendOrganization(ctx, s.pool, organizationID, reason); err != nil {
		return apperr.Internal("ORGANIZATION_SUSPEND_FAILED", "failed to suspend organization", err)
	}
	events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyOrganizationSuspended, "organization", organizationID,
		events.OrganizationSuspended{OrganizationID: organizationID, Reason: reason, SuspendedAt: time.Now()})
	return nil
}

// unsuspendOrganization is exposed cross-module (billing, error discarded)
// and internally by selfUnsuspendOrganization, which applies its own
// classification on top — see there.
func (s *service) unsuspendOrganization(ctx context.Context, organizationID string) error {
	if err := s.repo.unsuspendOrganization(ctx, s.pool, organizationID); err != nil {
		return err
	}
	events.Publish(ctx, s.pub, events.ExchangeOrganization, events.RoutingKeyOrganizationReactivated, "organization", organizationID,
		events.OrganizationReactivated{OrganizationID: organizationID, ReactivatedAt: time.Now()})
	return nil
}

// selfUnsuspendOrganization is the owner-facing HTTP route's entry point —
// unlike unsuspendOrganization (also called directly by billing on
// successful payment), this refuses to reverse a suspension it didn't
// self-trigger, so it can never be used to skip paying an open invoice.
// Every failure (including a not-found org from getOrganization) collapses
// to the same fixed ORGANIZATION_UNSUSPEND_FAILED, matching the
// pre-migration handler, which never special-cased ErrNoRows here.
func (s *service) selfUnsuspendOrganization(ctx context.Context, organizationID string) (err error) {
	defer func() {
		if err == nil {
			return
		}
		if errors.Is(err, ErrCannotSelfUnsuspendBillingHold) {
			err = apperr.Validation("BILLING_HOLD", ErrCannotSelfUnsuspendBillingHold.Error())
			return
		}
		err = apperr.Internal("ORGANIZATION_UNSUSPEND_FAILED", "failed to unsuspend organization", err)
	}()

	ws, err := s.getOrganization(ctx, organizationID)
	if err != nil {
		return err
	}
	if ws.SuspendedReason != suspendReasonSelfService {
		return ErrCannotSelfUnsuspendBillingHold
	}
	return s.unsuspendOrganization(ctx, organizationID)
}
