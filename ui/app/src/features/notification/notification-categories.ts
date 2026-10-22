import {
  IconBuilding,
  IconReceipt,
  IconUserCog,
  IconUsers,
  type TablerIcon,
} from "@tabler/icons-react"

// The backend's flat event keys group into 4 human categories
// (Organization / Members / Billing / Account). This grouping is a
// frontend-only static map — the backend stays flat
// (`notification.messages.channel`, `notification.preferences.event_type`).
//
// Two vocabularies exist and don't always share spelling: `channel` is what
// in-app messages are tagged with, `event_type` is the email template name
// checked by preferences. A couple of keys differ only in prefix (e.g.
// "welcome" vs "organization_welcome"), so each map stays a literal match
// against its own backend column rather than being merged into one.
export type NotificationCategory =
  "organization" | "members" | "billing" | "account"

// Shared between the feed rows and the preferences matrix so both views read
// as one system without needing brand color — icon shape is the only
// category signal in this monochrome theme.
export const CATEGORY_ICON: Record<NotificationCategory, TablerIcon> = {
  organization: IconBuilding,
  members: IconUsers,
  billing: IconReceipt,
  account: IconUserCog,
}

export const CATEGORIES: NotificationCategory[] = [
  "organization",
  "members",
  "billing",
  "account",
]

// The feed's own category tabs are only All/Organization/Members/Billing,
// per the app shell design — "account" notifications (e.g. email_changed)
// still show up under "All", just have no dedicated tab. The preferences
// matrix still shows all 4 categories, "account" included.
export const FEED_CATEGORIES: NotificationCategory[] = [
  "organization",
  "members",
  "billing",
]

export const CHANNEL_CATEGORY: Record<string, NotificationCategory> = {
  welcome: "organization",
  organization_suspended: "organization",
  organization_reactivated: "organization",
  organization_deleted: "organization",
  member_removed: "members",
  member_role_changed: "members",
  member_suspended: "members",
  member_reinstated: "members",
  invite: "members",
  invitation_requested: "members",
  ownership_transferred: "organization",
  trial_started: "billing",
  invoice_created: "billing",
  invoice_paid: "billing",
  invoice_failed: "billing",
  invoice_payment_remind: "billing",
  invoice_payment_final: "billing",
  subscription_activated: "billing",
  subscription_cancelled: "billing",
  subscription_resumed: "billing",
  subscription_remind: "billing",
  usage_limit_warning: "billing",
  webhook_health_warning: "organization",
  webhook_auto_disabled: "organization",
  email_changed: "account",
}

// Email preference event_type keys — only events that still send email
// belong here (organization_welcome, trial_started, invoice_created,
// subscription_activated/cancelled/resumed, member_role_changed,
// organization_ownership_transferred, and invitation_requested are in-app
// only, same as organization_suspended/organization_reactivated/
// email_changed always were, so they have no row here to gate).
export const EVENT_TYPE_CATEGORY: Record<string, NotificationCategory> = {
  organization_deleted: "organization",
  member_removed: "members",
  invite: "members",
  invoice_paid: "billing",
  invoice_failed: "billing",
  invoice_payment_remind: "billing",
  invoice_payment_final: "billing",
  subscription_remind: "billing",
  subscription_expired: "billing",
  usage_limit_warning: "billing",
  webhook_health_warning: "organization",
  webhook_auto_disabled: "organization",
}

export function channelsInCategory(category: NotificationCategory): string[] {
  return Object.keys(CHANNEL_CATEGORY).filter(
    (k) => CHANNEL_CATEGORY[k] === category
  )
}

export function eventTypesInCategory(category: NotificationCategory): string[] {
  return Object.keys(EVENT_TYPE_CATEGORY).filter(
    (k) => EVENT_TYPE_CATEGORY[k] === category
  )
}

// The one preference that isn't a pure opt-out: an
// organization owner cannot disable the payment-failure email, since it's
// the one notification that directly protects their subscription from
// silently lapsing.
export const LOCKED_OWNER_EVENT_TYPE = "invoice_failed"
export const LOCKED_OWNER_CHANNEL = "email"
