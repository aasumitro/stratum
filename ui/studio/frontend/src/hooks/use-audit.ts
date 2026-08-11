import { wailsError } from "@/lib/ui"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { AuditService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"
import type { AuditFilter } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

export const AUDIT_PAGE_SIZE = 50

export function useAuditEvents(projectId: string, filter: AuditFilter) {
  return useQuery({
    queryKey: ["audit", "events", projectId, filter],
    queryFn: () =>
      AuditService.ListEvents(projectId, { ...filter, limit: AUDIT_PAGE_SIZE }),
    staleTime: 15_000,
    retry: 1,
  })
}

export function useAuditCount(
  projectId: string,
  filter: Omit<AuditFilter, "limit" | "offset">
) {
  return useQuery({
    queryKey: ["audit", "count", projectId, filter],
    queryFn: () =>
      AuditService.CountEvents(projectId, { ...filter, limit: 0, offset: 0 }),
    staleTime: 15_000,
    retry: 1,
  })
}

export async function exportAuditCSV(
  projectId: string,
  filter: AuditFilter
): Promise<void> {
  try {
    const csv = await AuditService.ExportCSV(projectId, filter)
    const blob = new Blob([csv], { type: "text/csv" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = `audit-log-${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    toast.error(`Export failed: ${wailsError(err)}`)
  }
}
