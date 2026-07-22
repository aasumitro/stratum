import { IconTrash } from "@tabler/icons-react"
import type { DLQInfo } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { isDLQ, StateDot } from "./utils"

export function QueueTableSkeleton() {
  return (
    <div className="animate-pulse">
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 px-4 py-3 border-b">
          <div className="h-4 bg-muted rounded flex-1" />
          <div className="h-4 bg-muted rounded w-16" />
          <div className="h-4 bg-muted rounded w-12" />
          <div className="h-4 bg-muted rounded w-16" />
          <div className="h-4 bg-muted rounded w-20" />
          <div className="h-8 bg-muted rounded w-24" />
        </div>
      ))}
    </div>
  )
}

export function QueueTable({
  queues,
  onView,
  onPurge,
}: {
  queues: DLQInfo[]
  onView: (q: DLQInfo) => void
  onPurge: (q: DLQInfo) => void
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Queue</TableHead>
          <TableHead className="w-28 text-center">Messages</TableHead>
          <TableHead className="w-28 text-center">Consumers</TableHead>
          <TableHead className="w-24 text-center">Rate/s</TableHead>
          <TableHead className="w-24">State</TableHead>
          <TableHead className="w-44 text-right">Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {queues.map((queue) => (
          <TableRow key={queue.name}>
            <TableCell>
              <div className="flex items-center gap-2">
                <code className="font-mono text-xs">{queue.name}</code>
                {isDLQ(queue.name) && (
                  <Badge variant="secondary" className="text-[10px] h-4 px-1">DLQ</Badge>
                )}
              </div>
            </TableCell>
            <TableCell className="text-center">
              {queue.messages > 0 ? (
                <Badge
                  variant={queue.messages > 10 ? "destructive" : "secondary"}
                  className="tabular-nums"
                >
                  {queue.messages}
                </Badge>
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className="text-center">
              <span className="text-xs tabular-nums">{queue.consumers}</span>
            </TableCell>
            <TableCell className="text-center">
              <span className="text-xs tabular-nums text-muted-foreground">
                {queue.message_rate > 0 ? queue.message_rate.toFixed(1) : "—"}
              </span>
            </TableCell>
            <TableCell>
              <div className="flex items-center gap-2">
                <StateDot state={queue.state} />
                <span className="text-xs text-muted-foreground">{queue.state}</span>
              </div>
            </TableCell>
            <TableCell className="text-right">
              <div className="flex items-center justify-end gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 text-xs"
                  onClick={() => onView(queue)}
                >
                  View
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 text-xs text-destructive hover:text-destructive"
                  onClick={() => onPurge(queue)}
                  disabled={queue.messages === 0}
                >
                  <IconTrash className="size-3.5" />
                  Purge
                </Button>
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
