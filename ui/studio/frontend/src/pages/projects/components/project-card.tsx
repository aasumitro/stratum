import React from "react"
import { IconDotsVertical, IconEdit, IconTrash } from "@tabler/icons-react"
import type { Project } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useCardMetrics } from "@/hooks/use-dashboard"
import { useLastStatus } from "@/hooks/use-monitor"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

const HEALTH_DOT: Record<string, string> = {
  ok:       "bg-green-500",
  degraded: "bg-amber-500",
  down:     "bg-red-500",
}

const HEALTH_LABEL: Record<string, string> = {
  ok:       "Healthy",
  degraded: "Degraded",
  down:     "Down",
}

function formatMRR(cents: number): string {
  const dollars = cents / 100
  if (dollars >= 1000) return `$${(dollars / 1000).toFixed(1)}k`
  return `$${dollars.toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`
}

interface ProjectCardProps {
  project: Project
  onEdit: (project: Project) => void
  onDelete: (project: Project) => void
  onSelect: (project: Project) => void
}

export function ProjectCard({
  project,
  onEdit,
  onDelete,
  onSelect,
}: ProjectCardProps) {
  const { data: lastStatus } = useLastStatus(project.id)
  const { data: metrics } = useCardMetrics(project.id)

  const dotClass = lastStatus
    ? (HEALTH_DOT[lastStatus.status] ?? "bg-muted-foreground/40")
    : "bg-muted-foreground/30"

  const statusLabel = lastStatus
    ? (HEALTH_LABEL[lastStatus.status] ?? lastStatus.status)
    : "Not checked"

  const latencyLabel = lastStatus?.latency_ms != null && lastStatus.latency_ms > 0
    ? ` · ${lastStatus.latency_ms}ms`
    : ""

  const activeOrganizations = metrics?.active_organizations ?? null
  const totalSubscriptions = metrics?.subscriptions_by_status
    ?.reduce((sum, s) => sum + s.count, 0) ?? null
  const mrr = metrics?.mrr ?? null

  return (
    <div
      className="group relative flex flex-col gap-3 rounded-lg border bg-card p-5 shadow-sm transition-shadow hover:shadow-md cursor-pointer"
      style={{ borderLeftColor: project.color, borderLeftWidth: 4 }}
      onClick={() => onSelect(project)}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <span
            className="flex-shrink-0 size-3 rounded-full"
            style={{ backgroundColor: project.color }}
          />
          <h3 className="font-semibold text-sm truncate">{project.name}</h3>
        </div>

        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon"
                className="size-7 flex-shrink-0 opacity-0 group-hover:opacity-100 transition-opacity"
              />
            }
            onClick={(e: React.MouseEvent) => e.stopPropagation()}
          >
            <IconDotsVertical className="size-4" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onClick={(e: React.MouseEvent) => {
                e.stopPropagation()
                onEdit(project)
              }}
            >
              <IconEdit className="size-4 mr-2" />
              Edit
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              variant="destructive"
              onClick={(e: React.MouseEvent) => {
                e.stopPropagation()
                onDelete(project)
              }}
            >
              <IconTrash className="size-4 mr-2" />
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <p className="text-xs text-muted-foreground truncate">{project.api_url}</p>

      {/* Health status */}
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <span className={`size-1.5 rounded-full ${dotClass}`} />
        <span>{statusLabel}{latencyLabel}</span>
      </div>

      {/* Live metrics row */}
      <div className="flex items-center gap-3 pt-1 border-t text-xs text-muted-foreground">
        <span title="Active organizations">
          <span className="font-medium text-foreground">
            {activeOrganizations !== null ? activeOrganizations.toLocaleString() : "—"}
          </span>{" "}orgs
        </span>
        <span className="text-border">·</span>
        <span title="Total subscriptions">
          <span className="font-medium text-foreground">
            {totalSubscriptions !== null ? totalSubscriptions.toLocaleString() : "—"}
          </span>{" "}subs
        </span>
        <span className="text-border">·</span>
        <span title="Monthly recurring revenue">
          <span className="font-medium text-foreground">
            {mrr !== null ? formatMRR(mrr) : "—"}
          </span>{" "}MRR
        </span>
      </div>
    </div>
  )
}
