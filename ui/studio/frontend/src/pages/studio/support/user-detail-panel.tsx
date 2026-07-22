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
      <div className="animate-pulse flex flex-col gap-4">
        <div className="h-20 bg-muted rounded-lg" />
        <div className="h-40 bg-muted rounded-lg" />
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
      <div className="flex items-start gap-4 p-4 rounded-lg border bg-card">
        <div className="size-12 rounded-full bg-muted flex items-center justify-center text-lg font-semibold flex-shrink-0">
          {initials(detail.full_name)}
        </div>
        <div className="flex-1 min-w-0">
          <p className="font-semibold">{detail.full_name || "—"}</p>
          <p className="text-sm text-muted-foreground">{detail.email}</p>
          <div className="flex items-center gap-2 mt-1.5">
            <code className="text-xs text-muted-foreground bg-muted px-1.5 py-0.5 rounded truncate max-w-[260px]">
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
        <div className="text-right text-xs text-muted-foreground flex-shrink-0">
          <p>Last seen</p>
          <p className="font-medium text-foreground">{formatDate(detail.last_seen_at)}</p>
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
          <p className="text-sm text-muted-foreground py-4 text-center">No organizations</p>
        ) : (
          <div className="border rounded-lg overflow-hidden">
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
