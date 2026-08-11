import { IconCopy } from "@tabler/icons-react"
import { toast } from "sonner"
import { useUserDetail } from "@/hooks/use-support"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatDate, initials } from "./utils"
import { LoginHistorySection } from "./login-history-section"
import { OrganizationRow } from "./organization-row"

interface UserDetailPanelProps {
  projectId: string
  authSub: string
}

export function UserDetailPanel({ projectId, authSub }: UserDetailPanelProps) {
  const { data: detail, isLoading, error } = useUserDetail(projectId, authSub)

  if (isLoading) {
    return (
      <div className="flex animate-pulse flex-col gap-4">
        <div className="h-20 rounded-lg bg-muted" />
        <div className="h-40 rounded-lg bg-muted" />
      </div>
    )
  }

  if (error) {
    return <p className="text-sm text-destructive">{String(error)}</p>
  }

  if (!detail) return null

  return (
    <div className="flex flex-col gap-6">
      {/* Profile card */}
      <div className="flex items-start gap-4 rounded-lg border bg-card p-4">
        <div className="flex size-12 shrink-0 items-center justify-center rounded-full bg-muted text-lg font-semibold">
          {initials(detail.full_name)}
        </div>
        <div className="min-w-0 flex-1">
          <p className="font-semibold">{detail.full_name || "—"}</p>
          <p className="text-sm text-muted-foreground">{detail.email}</p>
          <div className="mt-1.5 flex items-center gap-2">
            <code className="max-w-65 truncate rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
              {detail.auth_sub}
            </code>
            <Button
              variant="ghost"
              size="sm"
              className="h-5 w-5 p-0"
              onClick={() => {
                navigator.clipboard.writeText(detail.auth_sub)
                toast.success("Copied to clipboard")
              }}
            >
              <IconCopy className="size-3" />
            </Button>
          </div>
        </div>
        <div className="shrink-0 text-right text-xs text-muted-foreground">
          <p>Last seen</p>
          <p className="font-medium text-foreground">
            {formatDate(detail.last_seen_at)}
          </p>
        </div>
      </div>

      {/* Login history */}
      <LoginHistorySection projectId={projectId} authSub={authSub} />

      {/* Organizations */}
      <div className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold">
          Organizations ({detail.organizations?.length ?? 0})
        </h2>

        {!detail.organizations || detail.organizations.length === 0 ? (
          <p className="py-4 text-center text-sm text-muted-foreground">
            No organizations
          </p>
        ) : (
          <div className="overflow-hidden rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Organization</TableHead>
                  <TableHead className="w-20">Role</TableHead>
                  <TableHead className="w-28">Plan</TableHead>
                  <TableHead className="w-24">Billing</TableHead>
                  <TableHead className="w-28">Trial / End</TableHead>
                  <TableHead className="w-48 text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {detail.organizations.map((org) => (
                  <OrganizationRow
                    key={org.id}
                    projectId={projectId}
                    authSub={authSub}
                    organization={org}
                  />
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </div>
  )
}
