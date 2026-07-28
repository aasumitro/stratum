import { createFileRoute } from "@tanstack/react-router"
import { OrganizationSettingsPage } from "@/features/organization/pages/organization-settings-page"

// `panel` drives which "View All" OverlayPanel is open (Webhooks, Audit
// Log). `actor`/`action`/`resource`/`range` are Audit Log's filters, kept
// URL-synced so state is refresh-safe/back-button-safe, and filtered
// links stay shareable.
export interface SettingsSearch {
  panel?: "webhooks" | "audit-log"
  webhookId?: string
  actor?: string
  action?: string
  resource?: string
  range?: "7d" | "30d" | "90d" | "all"
}

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings"
)({
  validateSearch: (search: Record<string, unknown>): SettingsSearch => ({
    panel: (["webhooks", "audit-log"] as const).includes(search.panel as never)
      ? (search.panel as SettingsSearch["panel"])
      : undefined,
    webhookId:
      typeof search.webhookId === "string" ? search.webhookId : undefined,
    actor: typeof search.actor === "string" ? search.actor : undefined,
    action: typeof search.action === "string" ? search.action : undefined,
    resource: typeof search.resource === "string" ? search.resource : undefined,
    range: (["7d", "30d", "90d", "all"] as const).includes(
      search.range as never
    )
      ? (search.range as SettingsSearch["range"])
      : undefined,
  }),
  component: OrganizationSettingsPage,
})
