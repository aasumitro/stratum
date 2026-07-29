package notification

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/mailer"
	"github.com/aasumitro/stratum/internal/platform/pdf"
)

// Worker consumes domain events and fans out notification messages.
type Worker struct {
	svc *service
}

// Idempotent wraps a Handle* method so a redelivered event (RabbitMQ
// guarantees at-least-once, not exactly-once) is skipped rather than
// double-sending its emails/in-app messages. Every notification consumer
// registration in internal/app/bootstrap/consumers.go must wrap its handler
// with this. Fails open when the body's envelope ID can't be read, rather
// than blocking delivery over a decode hiccup.
func (w *Worker) Idempotent(next func(ctx context.Context, body []byte) error) func(ctx context.Context, body []byte) error {
	return func(ctx context.Context, body []byte) error {
		id, err := events.EnvelopeID(body)
		if err != nil || id == "" {
			return next(ctx, body)
		}
		claimed, err := w.svc.claimEvent(ctx, id)
		if err != nil {
			return fmt.Errorf("notification.claimEvent: %w", err)
		}
		if !claimed {
			slog.Info("skipping already-processed event", "event_id", id)
			return nil
		}
		return next(ctx, body)
	}
}

// HandleOrganizationCreated sends a welcome in_app message and email to the organization owner.
func (w *Worker) HandleOrganizationCreated(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationCreated](body)
	if err != nil {
		return fmt.Errorf("notification.HandleOrganizationCreated: decode: %w", err)
	}
	sub := evt.CreatedBy
	_, err = w.svc.send(ctx, evt.OrganizationID, &sub, "in_app", "welcome",
		"Welcome to "+evt.Name,
		fmt.Sprintf("Your organization %q is ready. Invite your team to get started.", evt.Name),
		nil,
	)

	email, _, lang, _ := w.svc.resolveOwnerEmail(ctx, evt.OrganizationID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrganizationID, email, sub, "organization_welcome", lang, mailer.TemplateData{
			OrganizationName: evt.Name,
			ActionURL:        fmt.Sprintf("%s/organization/%s", w.svc.appURL, evt.OrganizationID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleOrganizationCreated: %w", err)
	}
	return nil
}

// HandleOrganizationSuspended notifies all organization members that the organization was suspended.
func (w *Worker) HandleOrganizationSuspended(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationSuspended](body)
	if err != nil {
		return fmt.Errorf("notification.HandleOrganizationSuspended: decode: %w", err)
	}
	if w.svc.orgReader == nil {
		return nil
	}
	members, err := w.svc.orgReader.ListMemberAuthSubs(ctx, evt.OrganizationID)
	if err != nil || len(members) == 0 {
		return nil
	}
	if err := w.svc.sendToMany(ctx, evt.OrganizationID, members, "in_app", "organization_suspended",
		"Organization suspended",
		fmt.Sprintf("Your organization has been suspended. Reason: %s", evt.Reason),
	); err != nil {
		return fmt.Errorf("notification.HandleOrganizationSuspended: %w", err)
	}
	return nil
}

// HandleOrganizationReactivated notifies all organization members that the organization is active again.
func (w *Worker) HandleOrganizationReactivated(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationReactivated](body)
	if err != nil {
		return fmt.Errorf("notification.HandleOrganizationReactivated: decode: %w", err)
	}
	if w.svc.orgReader == nil {
		return nil
	}
	members, err := w.svc.orgReader.ListMemberAuthSubs(ctx, evt.OrganizationID)
	if err != nil || len(members) == 0 {
		return nil
	}
	if err := w.svc.sendToMany(ctx, evt.OrganizationID, members, "in_app", "organization_reactivated",
		"Organization reactivated",
		"Your organization has been reactivated and is now active again.",
	); err != nil {
		return fmt.Errorf("notification.HandleOrganizationReactivated: %w", err)
	}
	return nil
}

// HandleInvoicePaid sends an in_app notification and a receipt email when an invoice is settled.
func (w *Worker) HandleInvoicePaid(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvoicePaid](body)
	if err != nil {
		return fmt.Errorf("notification.HandleInvoicePaid: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "invoice_paid",
		"Invoice paid",
		fmt.Sprintf("Invoice %s has been paid successfully.", evt.InvoiceID),
		nil,
	)

	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "invoice_paid", lang, mailer.TemplateData{
			OrganizationName: name,
			Amount:           pdf.FormatMoney(int64(evt.AmountCents), evt.Currency),
			DueDate:          evt.PaidAt.Format("Jan 2, 2006"),
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}

	if err != nil {
		return fmt.Errorf("notification.HandleInvoicePaid: %w", err)
	}
	return nil
}

// HandleOrganizationDeleted notifies every member (in-app + email) that their organization was deleted.
func (w *Worker) HandleOrganizationDeleted(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationDeleted](body)
	if err != nil {
		return fmt.Errorf("notification.HandleOrganizationDeleted: decode: %w", err)
	}
	if w.svc.orgReader == nil {
		return nil
	}
	members, err := w.svc.orgReader.ListMemberAuthSubs(ctx, evt.OrganizationID)
	if err != nil || len(members) == 0 {
		return nil
	}

	name := w.svc.resolveOrganizationName(ctx, evt.OrganizationID)
	errs := w.svc.sendToMany(ctx, evt.OrganizationID, members, "in_app", "organization_deleted",
		"Organization deleted",
		fmt.Sprintf("Your organization %q has been deleted.", name),
	)

	// Emails are still sent one at a time — each recipient's language
	// preference and rendered content differ, unlike the in_app fan-out
	// above, so there's nothing to batch into a single round trip.
	for _, sub := range members {
		email, lang := w.svc.resolveMemberEmail(ctx, sub)
		if email != "" {
			w.svc.sendEmail(ctx, evt.OrganizationID, email, sub, "organization_deleted", lang, mailer.TemplateData{
				OrganizationName: name,
			})
		}
	}
	if errs != nil {
		return fmt.Errorf("notification.HandleOrganizationDeleted: %w", errs)
	}
	return nil
}

// HandleSubscriptionRemind sends a reminder that the trial or subscription is expiring soon.
func (w *Worker) HandleSubscriptionRemind(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.SubscriptionCheck](body)
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionRemind: decode: %w", err)
	}

	inAppBody := fmt.Sprintf("Your subscription expires on %s. Please renew to avoid interruption.",
		evt.ExpectedEnd.Format("Jan 2, 2006"))
	if evt.IsTrial {
		inAppBody = fmt.Sprintf("Your free trial ends on %s. Add a payment method to keep access.",
			evt.ExpectedEnd.Format("Jan 2, 2006"))
	}
	_, err = w.svc.send(ctx, evt.SubjectID, w.svc.resolveOwnerAuthSub(ctx, evt.SubjectID), "in_app", "subscription_remind",
		"Subscription expiring soon", inAppBody, nil,
	)

	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.SubjectID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.SubjectID, email, ownerSub, "subscription_remind", lang, mailer.TemplateData{
			OrganizationName: name,
			PlanName:         evt.Plan,
			DueDate:          evt.ExpectedEnd.Format("Jan 2, 2006"),
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.SubjectID),
			IsTrial:          evt.IsTrial,
		})
	}

	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionRemind: %w", err)
	}
	return nil
}

// HandleTrialStarted notifies the organization owner that their trial has begun.
func (w *Worker) HandleTrialStarted(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.TrialStarted](body)
	if err != nil {
		return fmt.Errorf("notification.HandleTrialStarted: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "trial_started",
		"Trial started",
		fmt.Sprintf("Your trial is active until %s.", evt.TrialEnd.Format("Jan 2, 2006")),
		nil,
	)

	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "trial_started", lang, mailer.TemplateData{
			OrganizationName: name,
			PlanName:         evt.Plan,
			DueDate:          evt.TrialEnd.Format("Jan 2, 2006"),
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleTrialStarted: %w", err)
	}
	return nil
}

// HandleInvoiceCreated notifies that a renewal invoice has been generated.
func (w *Worker) HandleInvoiceCreated(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvoiceCreated](body)
	if err != nil {
		return fmt.Errorf("notification.HandleInvoiceCreated: decode: %w", err)
	}

	inAppBody := fmt.Sprintf("A renewal invoice %s has been created. Please complete payment to continue your subscription.", evt.InvoiceID)
	if evt.FromTrial {
		inAppBody = fmt.Sprintf("Your trial has ended — invoice %s is ready. Please complete payment to continue.", evt.InvoiceID)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "invoice_created",
		"Invoice generated", inAppBody, nil,
	)

	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "invoice_created", lang, mailer.TemplateData{
			OrganizationName: name,
			PlanName:         evt.Plan,
			Amount:           pdf.FormatMoney(evt.AmountCents, evt.Currency),
			DueDate:          evt.DueAt.Format("Jan 2, 2006"),
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
			FromTrial:        evt.FromTrial,
		})
	}

	if err != nil {
		return fmt.Errorf("notification.HandleInvoiceCreated: %w", err)
	}
	return nil
}

// HandleInvoiceFailed notifies the organization owner that a payment failed.
func (w *Worker) HandleInvoiceFailed(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvoiceFailed](body)
	if err != nil {
		return fmt.Errorf("notification.HandleInvoiceFailed: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "invoice_failed",
		"Payment failed",
		fmt.Sprintf("Payment for invoice %s has failed. Please update your payment method.", evt.InvoiceID),
		nil,
	)
	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "invoice_failed", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleInvoiceFailed: %w", err)
	}
	return nil
}

// HandleSubscriptionActivated notifies the organization owner that a plan is now active.
func (w *Worker) HandleSubscriptionActivated(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.SubscriptionActivated](body)
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionActivated: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "subscription_activated",
		"Subscription activated",
		fmt.Sprintf("Your %s plan subscription is now active.", evt.Plan),
		nil,
	)
	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "subscription_activated", lang, mailer.TemplateData{
			OrganizationName: name,
			PlanName:         evt.Plan,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionActivated: %w", err)
	}
	return nil
}

// HandleSubscriptionCancelled notifies the organization owner that the subscription was cancelled.
func (w *Worker) HandleSubscriptionCancelled(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.SubscriptionCancelled](body)
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionCancelled: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "subscription_cancelled",
		"Subscription cancelled",
		"Your subscription has been cancelled. Access continues until the end of the current period.",
		nil,
	)
	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "subscription_cancelled", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionCancelled: %w", err)
	}
	return nil
}

// HandleSubscriptionExpired notifies the organization owner that their
// subscription's trial or paid period lapsed unpaid — distinct from
// HandleSubscriptionCancelled: by the time this fires the organization has
// already been suspended (billing.expireIfDue), so the copy must not claim
// access continues.
func (w *Worker) HandleSubscriptionExpired(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.SubscriptionExpired](body)
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionExpired: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "subscription_expired",
		"Subscription expired",
		"Your subscription has expired and your organization has been suspended. Pay the outstanding invoice to restore access.",
		nil,
	)
	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "subscription_expired", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionExpired: %w", err)
	}
	return nil
}

// HandleSubscriptionResumed notifies the organization owner that the subscription was reactivated.
func (w *Worker) HandleSubscriptionResumed(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.SubscriptionResumed](body)
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionResumed: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "subscription_resumed",
		"Subscription reactivated",
		fmt.Sprintf("Your %s plan subscription has been reactivated.", evt.Plan),
		nil,
	)
	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "subscription_resumed", lang, mailer.TemplateData{
			OrganizationName: name,
			PlanName:         evt.Plan,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionResumed: %w", err)
	}
	return nil
}

// HandleSubscriptionPaymentRemind sends the day-3 dunning reminder for an unpaid invoice.
func (w *Worker) HandleSubscriptionPaymentRemind(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvoiceFailed](body)
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionPaymentRemind: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "invoice_payment_remind",
		"Payment still pending",
		fmt.Sprintf("Invoice %s is still unpaid. Please complete payment to keep your subscription active.", evt.InvoiceID),
		nil,
	)
	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "invoice_payment_remind", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionPaymentRemind: %w", err)
	}
	return nil
}

// HandleSubscriptionPaymentFinal sends the day-7 final dunning warning before suspension.
func (w *Worker) HandleSubscriptionPaymentFinal(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvoiceFailed](body)
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionPaymentFinal: decode: %w", err)
	}
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID), "in_app", "invoice_payment_final",
		"Final payment reminder",
		fmt.Sprintf("This is your final notice for invoice %s. Your subscription may be suspended if payment is not received.", evt.InvoiceID),
		nil,
	)
	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "invoice_payment_final", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleSubscriptionPaymentFinal: %w", err)
	}
	return nil
}

// HandleMemberInvited sends an invite email to the invited person, and —
// when that email already belongs to a registered account — an in-app
// notification too, so an existing user isn't stuck needing the email link
// to discover the invite.
// authSub is empty for the email send — invitee may not be registered yet,
// so no preference check.
func (w *Worker) HandleMemberInvited(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.MemberInvited](body)
	if err != nil {
		return fmt.Errorf("notification.HandleMemberInvited: decode: %w", err)
	}

	orgName := evt.OrganizationID
	locale := "en"
	if w.svc.orgReader != nil {
		if ws, _ := w.svc.orgReader.GetOrganizationByID(ctx, evt.OrganizationID); ws != nil {
			orgName = ws.Name
			locale = ws.Locale
		}
	}

	w.svc.sendEmail(ctx, evt.OrganizationID, evt.Email, "", "invite", locale, mailer.TemplateData{
		OrganizationName: orgName,
		InviteURL:        fmt.Sprintf("%s/invitations/accept?token=%s", w.svc.appURL, evt.Token),
	})

	if w.svc.userReader != nil {
		if user, err := w.svc.userReader.GetUserByEmail(ctx, evt.Email); err == nil && user != nil {
			if _, err := w.svc.send(ctx, evt.OrganizationID, &user.AuthSub, "in_app", "invite",
				"You've been invited",
				fmt.Sprintf("You've been invited to join %q.", orgName),
				nil,
			); err != nil {
				slog.Warn("in-app invite notification failed", "org_id", evt.OrganizationID, "auth_sub", user.AuthSub, "error", err)
			}
		}
	}

	return nil
}

// HandleInvitationRequested emails the ORIGINAL inviter (not the requester)
// that a lost/expired invitation needs resending — the requester has no
// permission to re-invite themselves.
func (w *Worker) HandleInvitationRequested(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvitationRequested](body)
	if err != nil {
		return fmt.Errorf("notification.HandleInvitationRequested: decode: %w", err)
	}
	name := w.svc.resolveOrganizationName(ctx, evt.OrganizationID)
	email, lang := w.svc.resolveMemberEmail(ctx, evt.InvitedBy)
	if email == "" {
		return nil
	}
	w.svc.sendEmail(ctx, evt.OrganizationID, email, evt.InvitedBy, "invitation_requested", lang, mailer.TemplateData{
		OrganizationName: name,
		UserName:         evt.InviteeEmail, // repurposed here to carry the requester's email — see TemplateData.UsageDetail for the same one-off-field convention
	})
	return nil
}

// HandleInvitationDeclined notifies the original inviter (in-app only —
// not urgent enough to also warrant an email interruption) that the
// invitee declined.
func (w *Worker) HandleInvitationDeclined(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvitationDeclined](body)
	if err != nil {
		return fmt.Errorf("notification.HandleInvitationDeclined: decode: %w", err)
	}
	name := w.svc.resolveOrganizationName(ctx, evt.OrganizationID)
	sub := evt.InvitedBy
	_, err = w.svc.send(ctx, evt.OrganizationID, &sub, "in_app", "invitation_declined",
		"Invitation declined",
		fmt.Sprintf("%s declined your invitation to join %q.", evt.InviteeEmail, name),
		nil,
	)
	if err != nil {
		return fmt.Errorf("notification.HandleInvitationDeclined: %w", err)
	}
	return nil
}

// HandleMemberRemoved notifies the removed member (in-app + email), not the whole organization.
func (w *Worker) HandleMemberRemoved(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.MemberRemoved](body)
	if err != nil {
		return fmt.Errorf("notification.HandleMemberRemoved: decode: %w", err)
	}
	name := w.svc.resolveOrganizationName(ctx, evt.OrganizationID)
	sub := evt.AuthSub
	_, err = w.svc.send(ctx, evt.OrganizationID, &sub, "in_app", "member_removed",
		"Removed from organization",
		fmt.Sprintf("You have been removed from %q.", name),
		nil,
	)

	email, lang := w.svc.resolveMemberEmail(ctx, evt.AuthSub)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrganizationID, email, evt.AuthSub, "member_removed", lang, mailer.TemplateData{
			OrganizationName: name,
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleMemberRemoved: %w", err)
	}
	return nil
}

// HandleMemberRoleChanged notifies the affected member (in-app + email) that their role changed.
func (w *Worker) HandleMemberRoleChanged(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.MemberRoleChanged](body)
	if err != nil {
		return fmt.Errorf("notification.HandleMemberRoleChanged: decode: %w", err)
	}
	name := w.svc.resolveOrganizationName(ctx, evt.OrganizationID)
	sub := evt.AuthSub
	_, err = w.svc.send(ctx, evt.OrganizationID, &sub, "in_app", "member_role_changed",
		"Role changed",
		fmt.Sprintf("Your role in %q has been changed to %s.", name, evt.Role),
		nil,
	)

	email, lang := w.svc.resolveMemberEmail(ctx, evt.AuthSub)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrganizationID, email, evt.AuthSub, "member_role_changed", lang, mailer.TemplateData{
			OrganizationName: name,
			Role:             evt.Role,
			ActionURL:        fmt.Sprintf("%s/organization/%s", w.svc.appURL, evt.OrganizationID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleMemberRoleChanged: %w", err)
	}
	return nil
}

// HandleOwnershipTransferred notifies the new owner (in-app + email) — the outgoing owner is not notified.
func (w *Worker) HandleOwnershipTransferred(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OwnershipTransferred](body)
	if err != nil {
		return fmt.Errorf("notification.HandleOwnershipTransferred: decode: %w", err)
	}
	name := w.svc.resolveOrganizationName(ctx, evt.OrganizationID)
	sub := evt.NewOwner
	_, err = w.svc.send(ctx, evt.OrganizationID, &sub, "in_app", "ownership_transferred",
		"You are now the owner",
		fmt.Sprintf("You are now the owner of %q.", name),
		nil,
	)

	email, lang := w.svc.resolveMemberEmail(ctx, evt.NewOwner)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrganizationID, email, evt.NewOwner, "organization_ownership_transferred", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s", w.svc.appURL, evt.OrganizationID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleOwnershipTransferred: %w", err)
	}
	return nil
}

// HandleWebhookHealthWarning notifies the organization owner (in-app +
// email) the first time a webhook endpoint's 24h success rate drops below
// 70% — organization.checkHealth only fires this once per 24h per
// endpoint, so no additional de-duplication is needed here.
func (w *Worker) HandleWebhookHealthWarning(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.WebhookHealthWarning](body)
	if err != nil {
		return fmt.Errorf("notification.HandleWebhookHealthWarning: decode: %w", err)
	}
	email, name, lang, sub := w.svc.resolveOwnerEmail(ctx, evt.OrganizationID)
	if sub == "" {
		return nil
	}
	_, err = w.svc.send(ctx, evt.OrganizationID, &sub, "in_app", "webhook_health_warning",
		"Webhook delivery issues",
		fmt.Sprintf("Your webhook endpoint %s is only succeeding %d%% of deliveries in the last 24 hours.", evt.URL, evt.SuccessPercent),
		nil,
	)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrganizationID, email, sub, "webhook_health_warning", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s/webhooks", w.svc.appURL, evt.OrganizationID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleWebhookHealthWarning: %w", err)
	}
	return nil
}

// HandleWebhookAutoDisabled notifies the organization owner (in-app +
// email) when a webhook endpoint is automatically disabled after 3 days of
// 100% delivery failure.
func (w *Worker) HandleWebhookAutoDisabled(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.WebhookAutoDisabled](body)
	if err != nil {
		return fmt.Errorf("notification.HandleWebhookAutoDisabled: decode: %w", err)
	}
	email, name, lang, sub := w.svc.resolveOwnerEmail(ctx, evt.OrganizationID)
	if sub == "" {
		return nil
	}
	_, err = w.svc.send(ctx, evt.OrganizationID, &sub, "in_app", "webhook_auto_disabled",
		"Webhook disabled",
		fmt.Sprintf("Your webhook endpoint %s was automatically disabled after 3 days of failed deliveries. Send a test event to re-enable it.", evt.URL),
		nil,
	)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrganizationID, email, sub, "webhook_auto_disabled", lang, mailer.TemplateData{
			OrganizationName: name,
			ActionURL:        fmt.Sprintf("%s/organization/%s/webhooks", w.svc.appURL, evt.OrganizationID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleWebhookAutoDisabled: %w", err)
	}
	return nil
}

// HandleUserEmailChanged sends an in-app security notice when an account's
// email is changed. No email here — Supabase's own Secure Email Change
// confirmation emails already cover that; a second email from us would be
// redundant.
func (w *Worker) HandleUserEmailChanged(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.UserEmailChanged](body)
	if err != nil {
		return fmt.Errorf("notification.HandleUserEmailChanged: decode: %w", err)
	}
	orgID := w.svc.resolveFirstOrganizationID(ctx, evt.AuthSub)
	if orgID == "" {
		return nil
	}
	sub := evt.AuthSub
	_, err = w.svc.send(ctx, orgID, &sub, "in_app", "email_changed",
		"Email address changed",
		fmt.Sprintf("Your account email was changed from %s to %s. If this wasn't you, contact support immediately.",
			evt.OldEmail, evt.NewEmail),
		nil,
	)
	if err != nil {
		return fmt.Errorf("notification.HandleUserEmailChanged: %w", err)
	}
	return nil
}

// HandleUsageLimitWarning notifies the organization owner when a metered
// feature crosses 90% of its effective limit (plan + any attached addon
// delta), giving them a proactive warning before they hit a hard cap.
func (w *Worker) HandleUsageLimitWarning(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.UsageLimitWarning](body)
	if err != nil {
		return fmt.Errorf("notification.HandleUsageLimitWarning: decode: %w", err)
	}
	detail := fmt.Sprintf("%d / %d", evt.Current, evt.Limit)
	_, err = w.svc.send(ctx, evt.OrgID, w.svc.resolveOwnerAuthSub(ctx, evt.OrgID),
		"in_app", "usage_limit_warning",
		"Approaching usage limit",
		fmt.Sprintf("%s usage is at %s.", evt.Metric, detail),
		nil,
	)

	email, name, lang, ownerSub := w.svc.resolveOwnerEmail(ctx, evt.OrgID)
	if email != "" {
		w.svc.sendEmail(ctx, evt.OrgID, email, ownerSub, "usage_limit_warning", lang, mailer.TemplateData{
			OrganizationName: name,
			UsageDetail:      detail,
			ActionURL:        fmt.Sprintf("%s/organization/%s/billing", w.svc.appURL, evt.OrgID),
		})
	}
	if err != nil {
		return fmt.Errorf("notification.HandleUsageLimitWarning: %w", err)
	}
	return nil
}
