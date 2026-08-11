import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  IconBuildingCommunity,
  IconChevronLeft,
  IconChevronRight,
  IconClipboard,
  IconLock,
  IconLockOpen,
  IconSearch,
} from "@tabler/icons-react"
import type { OrganizationSummary } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  PAGE_SIZE,
  useOrganizationCount,
  useOrganizations,
} from "@/hooks/use-organization-ops"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/ui"
import { SuspendDialog } from "./suspend-dialog"
import { billingBadge, statusBadge, type StatusFilter } from "./badges"
import { UnsuspendDialog } from "./unsuspend-dialog"

export function OrganizationsPage() {
  const { projectId } = useParams({ from: "/studio/$projectId/organizations" })

  const [statusFilter, setStatusFilter] = useState<StatusFilter>("")
  const [search, setSearch] = useState("")
  const [offset, setOffset] = useState(0)
  const [suspendTarget, setSuspendTarget] =
    useState<OrganizationSummary | null>(null)
  const [unsuspendTarget, setUnsuspendTarget] =
    useState<OrganizationSummary | null>(null)

  const {
    data: organizations,
    isLoading,
    error,
    refetch,
  } = useOrganizations(projectId, statusFilter, offset)
  const { data: total } = useOrganizationCount(projectId, statusFilter)

  const filtered = search.trim()
    ? (organizations ?? []).filter(
        (o) =>
          o.name.toLowerCase().includes(search.toLowerCase()) ||
          o.slug.toLowerCase().includes(search.toLowerCase())
      )
    : (organizations ?? [])

  const hasMore = (organizations?.length ?? 0) === PAGE_SIZE
  const hasPrev = offset > 0
  const currentPage = Math.floor(offset / PAGE_SIZE) + 1

  const STATUS_OPTIONS: { value: StatusFilter; label: string }[] = [
    { value: "", label: "All" },
    { value: "active", label: "Active" },
    { value: "suspended", label: "Suspended" },
    { value: "deleted", label: "Deleted" },
  ]

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-center gap-3 border-b px-6 py-4">
        <IconBuildingCommunity className="size-4 text-muted-foreground" />
        <div className="flex flex-col gap-0.5">
          <h1 className="text-sm font-semibold">Organizations</h1>
          <p className="text-xs text-muted-foreground">
            Manage all organizations across this project
            {total !== undefined && (
              <span className="ml-1 font-medium text-foreground">
                ({total})
              </span>
            )}
          </p>
        </div>
      </header>

      <div className="space-y-5 p-6">
        {/* Filter bar */}
        <div className="flex items-center gap-3">
          <div className="relative max-w-sm flex-1">
            <IconSearch className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-8"
              placeholder="Search by name or slug…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>

          <div className="flex items-center gap-1 rounded-md border bg-muted/40 p-0.5">
            {STATUS_OPTIONS.map((opt) => (
              <button
                key={opt.value}
                type="button"
                onClick={() => {
                  setStatusFilter(opt.value)
                  setOffset(0)
                }}
                className={cn(
                  "rounded px-3 py-1 text-sm transition-colors",
                  statusFilter === opt.value
                    ? "bg-background font-medium text-foreground shadow"
                    : "text-muted-foreground hover:text-foreground"
                )}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </div>

        {/* Table */}
        <div className="rounded-md border">
          {isLoading ? (
            <div className="divide-y">
              {Array.from({ length: 6 }).map((_, i) => (
                <div key={i} className="px-4 py-3">
                  <Skeleton className="h-5 w-full" />
                </div>
              ))}
            </div>
          ) : error ? (
            <div className="flex flex-col items-center gap-2 py-12 text-sm text-muted-foreground">
              <span>Failed to load organizations</span>
              <Button variant="outline" size="sm" onClick={() => refetch()}>
                Try again
              </Button>
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
              <IconBuildingCommunity className="size-8 opacity-30" />
              <span className="text-sm">No organizations found</span>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Slug</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Plan</TableHead>
                  <TableHead>Billing</TableHead>
                  <TableHead className="text-right">Members</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((org) => (
                  <TableRow key={org.id}>
                    <TableCell className="max-w-40 truncate font-medium">
                      {org.name}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {org.slug}
                    </TableCell>
                    <TableCell>{statusBadge(org.status)}</TableCell>
                    <TableCell>
                      {org.plan_name ? (
                        <span className="text-sm">{org.plan_name}</span>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell>{billingBadge(org.billing_status)}</TableCell>
                    <TableCell className="text-right text-sm">
                      {org.member_count}
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {new Date(org.created_at).toLocaleDateString()}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 w-7 p-0 text-muted-foreground"
                          title="Copy organization ID"
                          onClick={() => {
                            void navigator.clipboard.writeText(org.id).then(
                              () => toast.success("Organization ID copied"),
                              () => toast.error("Copy failed")
                            )
                          }}
                        >
                          <IconClipboard className="size-3.5" />
                        </Button>
                        {org.status === "active" && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="gap-1.5 text-amber-600 hover:bg-amber-50 hover:text-amber-700 dark:hover:bg-amber-950"
                            onClick={() => setSuspendTarget(org)}
                          >
                            <IconLock className="size-3.5" />
                            Suspend
                          </Button>
                        )}
                        {org.status === "suspended" && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="gap-1.5 text-green-600 hover:bg-green-50 hover:text-green-700 dark:hover:bg-green-950"
                            onClick={() => setUnsuspendTarget(org)}
                          >
                            <IconLockOpen className="size-3.5" />
                            Restore
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        {/* Pagination */}
        {!isLoading && !error && (hasPrev || hasMore) && (
          <div className="flex items-center justify-between text-sm text-muted-foreground">
            <span>Page {currentPage}</span>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={!hasPrev}
                onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              >
                <IconChevronLeft className="size-4" />
                Previous
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={!hasMore}
                onClick={() => setOffset(offset + PAGE_SIZE)}
              >
                Next
                <IconChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        )}

        {/* Dialogs */}
        {suspendTarget && (
          <SuspendDialog
            organization={suspendTarget}
            projectId={projectId}
            statusFilter={statusFilter}
            onClose={() => setSuspendTarget(null)}
          />
        )}
        {unsuspendTarget && (
          <UnsuspendDialog
            organization={unsuspendTarget}
            projectId={projectId}
            statusFilter={statusFilter}
            onClose={() => setUnsuspendTarget(null)}
          />
        )}
      </div>
    </div>
  )
}
