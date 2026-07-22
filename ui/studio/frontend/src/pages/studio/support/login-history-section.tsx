import { useState } from "react"
import { IconChevronDown, IconChevronRight } from "@tabler/icons-react"
import { useUserLoginHistory } from "@/hooks/use-support"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatDateTime } from "./utils"

interface LoginHistorySectionProps {
  projectId: string
  authSub: string
}

export function LoginHistorySection({ projectId, authSub }: LoginHistorySectionProps) {
  const [expanded, setExpanded] = useState(false)
  const { data: events, isLoading } = useUserLoginHistory(projectId, authSub, expanded)

  return (
    <div className="flex flex-col gap-2">
      <button
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-1.5 text-sm font-semibold hover:text-foreground/80 transition-colors"
      >
        {expanded ? <IconChevronDown className="size-3.5" /> : <IconChevronRight className="size-3.5" />}
        Login History
      </button>

      {expanded && (
        isLoading ? (
          <p className="text-xs text-muted-foreground py-2">Loading…</p>
        ) : !events || events.length === 0 ? (
          <p className="text-xs text-muted-foreground py-2">No login history</p>
        ) : (
          <div className="border rounded-lg overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>IP address</TableHead>
                  <TableHead>Device</TableHead>
                  <TableHead className="text-right">Date</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.map((e, i) => (
                  <TableRow key={i}>
                    <TableCell className="text-xs font-mono py-1.5">{e.ip_address || "—"}</TableCell>
                    <TableCell className="text-xs py-1.5 text-muted-foreground truncate max-w-[280px]">
                      {e.user_agent || "—"}
                    </TableCell>
                    <TableCell className="text-xs py-1.5 text-right text-muted-foreground">
                      {formatDateTime(e.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )
      )}
    </div>
  )
}
