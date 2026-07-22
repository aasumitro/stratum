// Per-event subscriptions. This is the exhaustive list of event types
// the outbound webhook worker actually fans out (internal/app/worker.go's
// "organization.webhook-organization" / "organization.webhook-billing"
// consumer bindings) — deliberately not the full domain-event catalog,
// since subscribing to an event that's never delivered would silently do
// nothing. Frontend-only static map, same convention as
// notification-categories.ts; the backend stores subscribed_events as a
// plain TEXT[], no matching enum.
export interface WebhookEventCatalogEntry {
  key: string
  group: "organization" | "billing"
}

export const WEBHOOK_EVENT_CATALOG: WebhookEventCatalogEntry[] = [
  { key: "organization.created", group: "organization" },
  { key: "organization.member.invited", group: "organization" },
  { key: "billing.invoice.created", group: "billing" },
  { key: "billing.invoice.paid", group: "billing" },
  { key: "billing.invoice.failed", group: "billing" },
  { key: "billing.subscription.activated", group: "billing" },
  { key: "billing.subscription.cancelled", group: "billing" },
  { key: "billing.subscription.resumed", group: "billing" },
]

export const WEBHOOK_EVENT_GROUPS = ["organization", "billing"] as const
