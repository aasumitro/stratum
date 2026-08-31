package organization

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
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

// ErrIPAllowlistLocksOutCaller - saving this CIDR list would reject
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

	if err := s.validateCatalogSelections(ctx, plan, addons, couponCode, ownerID); err != nil {
		return nil, err
	}

	eventAddons := make([]events.AddonSelection, len(addons))
	for i, a := range addons {
		eventAddons[i] = events.AddonSelection{AddonID: a.AddonID, Quantity: a.Quantity}
	}

	var t *organizationRecord
	err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		var err error
		t, err = s.insertOrganizationWithOwner(ctx, tx, slug, name, ownerID, countryCode)
		if err != nil {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization, events.RoutingKeyOrganizationCreated, "organization", t.ID,
			events.OrganizationCreated{
				OrganizationID: t.ID, Slug: t.Slug, Name: t.Name, CreatedBy: t.OwnerID,
				CountryCode: t.CountryCode, Plan: plan, Cycle: cycle,
				Addons: eventAddons, CouponCode: couponCode, CreatedAt: t.CreatedAt,
			})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// validateCatalogSelections checks plan, every addon ID, and a non-empty
// coupon code against the live catalog before any DB write happens, so a
// bad selection never leaves a half-created organization (or one whose
// subscription-provisioning event fails forever) behind. A no-op when no
// catalogReader is wired — catalog validation is optional, matching every
// other call site of this dependency in this module.
func (s *service) validateCatalogSelections(
	ctx context.Context, plan string, addons []addonSelection, couponCode, ownerID string,
) error {
	if s.catalogReader == nil {
		return nil
	}
	if _, err := s.catalogReader.GetPlanByID(ctx, plan); err != nil {
		return ErrUnknownPlan
	}
	for _, a := range addons {
		if _, err := s.catalogReader.GetAddonByID(ctx, a.AddonID); err != nil {
			return ErrUnknownAddon
		}
	}
	if couponCode != "" {
		if err := s.catalogReader.ValidateCouponCode(ctx, couponCode, ownerID); err != nil {
			return ErrInvalidCoupon
		}
	}
	return nil
}

// insertOrganizationWithOwner creates the organization row and its owner
// membership row atomically — either both exist or neither does, since a
// membership-less organization would leave its owner unable to access what
// they just created. tx is the caller's own transaction (createOrganization's
// db.WithTx) so the OrganizationCreated outbox row enqueued alongside these
// two inserts commits or rolls back with them as one unit.
func (s *service) insertOrganizationWithOwner(
	ctx context.Context, tx db.Querier, slug, name, ownerID, countryCode string,
) (*organizationRecord, error) {
	t, err := s.repo.insertOrganization(ctx, tx, slug, name, ownerID, countryCode)
	if err != nil {
		return nil, fmt.Errorf("organization.insertOrganizationWithOwner: %w", err)
	}
	if err := s.repo.insertOwnerMembership(ctx, tx, t.ID, ownerID); err != nil {
		return nil, fmt.Errorf("organization.insertOrganizationWithOwner: %w", err)
	}
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

	// Logo cleanup used to run synchronously here — moved off the request
	// path to HandleOrganizationDeleted (service_organization.go, same file,
	// below), a worker consumer of the same OrganizationDeleted event
	// enqueued below. The organization is already soft-deleted by this
	// point, so the gap between this enqueue and the worker picking it up is
	// invisible to callers — the organization middleware already rejects
	// every request against a soft-deleted org.
	if err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if err := s.repo.softDeleteOrganization(ctx, tx, id); err != nil {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization, events.RoutingKeyOrganizationDeleted, "organization", id,
			events.OrganizationDeleted{OrganizationID: id, DeletedAt: time.Now()})
	}); err != nil {
		return fmt.Errorf("organization.deleteOrganization: %w", err)
	}
	return nil
}

// HandleOrganizationDeleted performs the logo cleanup deleteOrganization
// used to do inline on the request path (see the comment there) — moved off
// that path so the request doesn't wait on an object-storage round trip.
// Naturally idempotent against RabbitMQ's at-least-once redelivery: a
// repeated storage Delete on an already-missing object is a safe no-op — no
// dedup tracking needed here, unlike notification's worker.
func (w *WebhookWorker) HandleOrganizationDeleted(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationDeleted](body)
	if err != nil {
		w.log.Warn("organization deleted: malformed event", "error", err)
		return nil // don't re-queue a bad envelope
	}
	if w.store == nil {
		return nil
	}

	_ = w.store.Delete(ctx, "organization", evt.OrganizationID+"/logo")
	return nil
}

func (s *service) uploadLogo(ctx context.Context, organizationID string, r io.Reader, contentType string) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Internal("LOGO_UPLOAD_FAILED", "failed to upload logo", err)
		}
	}()

	if s.store == nil {
		return fmt.Errorf("organization.uploadLogo: storage not configured")
	}
	path := organizationID + "/logo"
	if err := s.store.Upload(ctx, "organization", path, r, contentType); err != nil {
		return fmt.Errorf("organization.uploadLogo: %w", err)
	}
	logoURL := fmt.Sprintf("%s/storage/v1/object/public/organization/%s", strings.TrimSuffix(s.store.BaseURL(), "/"), path)
	return s.repo.updateLogoURL(ctx, s.pool, organizationID, logoURL)
}

// localeFormat is a BCP-47 language(-REGION) shape check: two or three
// letters, optionally followed by a two-letter region ("en", "en-US",
// "id"). Deliberately a format check rather than a curated allowlist —
// there is no locale catalog to draw from, and this rejects injection
// payloads and typos without a list that goes stale.
var localeFormat = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z]{2})?$`)

// validateSettings rejects a timezone the Go runtime can't resolve and a
// locale outside the BCP-47 shape, so a bogus value is never persisted.
// timezone "" resolves to UTC without error, but the request struct already
// requires a non-empty value, so that case never reaches here.
func validateSettings(timezone, locale string) error {
	if _, err := time.LoadLocation(timezone); err != nil {
		return apperr.Validation("INVALID_TIMEZONE", "invalid timezone")
	}
	if !localeFormat.MatchString(locale) {
		return apperr.Validation("INVALID_LOCALE", "invalid locale")
	}
	return nil
}

func (s *service) updateSettings(
	ctx context.Context,
	organizationID, timezone, locale, callerIP string,
	allowedIPs *[]string,
) error {
	if err := validateSettings(timezone, locale); err != nil {
		return err
	}
	// A nil allowedIPs means the field was omitted from the request — nothing
	// to lock anyone out of, since the allowlist isn't changing.
	if allowedIPs != nil && ipLocksOutCaller(callerIP, *allowedIPs) {
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
// unparseable IP through a CIDR check. Per-entry matching delegates to
// middleware.IPEntryMatches — the same comparison the request-time allowlist
// gate uses, so this pre-save check can never approve a save that the gate
// would then reject.
func ipLocksOutCaller(callerIP string, allowedIPs []string) bool {
	if len(allowedIPs) == 0 {
		return false
	}
	ip := net.ParseIP(callerIP)
	if ip == nil {
		return true
	}
	for _, entry := range allowedIPs {
		if middleware.IPEntryMatches(ip, entry) {
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
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if err := s.repo.suspendOrganization(ctx, tx, organizationID, reason); err != nil {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization, events.RoutingKeyOrganizationSuspended, "organization", organizationID,
			events.OrganizationSuspended{OrganizationID: organizationID, Reason: reason, SuspendedAt: time.Now()})
	})
	if err != nil {
		return apperr.Internal("ORGANIZATION_SUSPEND_FAILED", "failed to suspend organization", err)
	}
	return nil
}

// unsuspendOrganization is exposed cross-module (billing, error discarded)
// and internally by selfUnsuspendOrganization, which applies its own
// classification on top — see there.
func (s *service) unsuspendOrganization(ctx context.Context, organizationID string) error {
	return db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if err := s.repo.unsuspendOrganization(ctx, tx, organizationID); err != nil {
			return err
		}
		return events.Enqueue(ctx, tx, events.ExchangeOrganization, events.RoutingKeyOrganizationReactivated, "organization", organizationID,
			events.OrganizationReactivated{OrganizationID: organizationID, ReactivatedAt: time.Now()})
	})
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
		return fmt.Errorf("organization.selfUnsuspendOrganization: %w", err)
	}
	if ws.SuspendedReason != suspendReasonSelfService {
		return ErrCannotSelfUnsuspendBillingHold
	}
	return s.unsuspendOrganization(ctx, organizationID)
}
