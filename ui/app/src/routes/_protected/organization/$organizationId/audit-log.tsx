import { createFileRoute } from "@tanstack/react-router"
import { AuditLogPage } from "@/features/organization/pages/audit-log-page"

// URL-synced filter chips — shareable links.
export interface AuditLogSearch {
  actor?: string
  action?: string
  resource?: string
  range?: "7d" | "30d" | "90d" | "all"
}

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/audit-log"
)({
  validateSearch: (search: Record<string, unknown>): AuditLogSearch => ({
    actor: typeof search.actor === "string" ? search.actor : undefined,
    action: typeof search.action === "string" ? search.action : undefined,
    resource: typeof search.resource === "string" ? search.resource : undefined,
    range: (["7d", "30d", "90d", "all"] as const).includes(
      search.range as never
    )
      ? (search.range as AuditLogSearch["range"])
      : undefined,
  }),
  component: AuditLogPage,
})
