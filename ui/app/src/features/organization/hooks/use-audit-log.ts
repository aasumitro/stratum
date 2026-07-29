import { useHTTPQuery } from "@/lib/api/query"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import type { AuditEvent } from "@/types/organization"

/** Chip filters — every field optional, all sent as query params to
 * both the list and export routes so export always matches what's on screen. */
export interface AuditLogFilter {
  actor?: string
  action?: string
  resource?: string
  from?: string
  to?: string
}

function auditLogParams(filter: AuditLogFilter, extra: Record<string, string>) {
  const params = new URLSearchParams(extra)
  if (filter.actor) params.set("actor", filter.actor)
  if (filter.action) params.set("action", filter.action)
  if (filter.resource) params.set("resource", filter.resource)
  if (filter.from) params.set("from", filter.from)
  if (filter.to) params.set("to", filter.to)
  return params
}

export function useAuditLog(
  organizationId: string,
  filter: AuditLogFilter = {},
  cursor?: string,
  limit = 25,
  enabled = true
) {
  const params = auditLogParams(filter, { limit: String(limit) })
  params.set("cursor", cursor ?? "")
  return useHTTPQuery<AuditEvent[]>({
    queryKey: [
      ...queryKeys.organizations.auditLog(organizationId),
      filter,
      cursor,
      limit,
    ],
    url: `${API.organizations(organizationId, "audit-log")}?${params.toString()}`,
    options: { enabled },
  })
}

export function auditLogExportUrl(
  organizationId: string,
  filter: AuditLogFilter
) {
  const qs = auditLogParams(filter, {}).toString()
  return `${API.organizations(organizationId, "audit-log", "export")}${qs ? `?${qs}` : ""}`
}
