import { useState } from "react"
import { IconAlertTriangle, IconMailExclamation, IconRefresh, IconRotateClockwise } from "@tabler/icons-react"
import type { DLQInfo, DLQMessage, RequeueResult } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useMessages, useRequeueMessage } from "@/hooks/use-dlq"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

interface MessagePanelProps {
  projectId: string
  queue: DLQInfo
  onClose: () => void
}

export function MessagePanel({ projectId, queue, onClose }: MessagePanelProps) {
  const { data: messages, isLoading, refetch } = useMessages(projectId, queue.name)
  const requeue = useRequeueMessage(projectId)
  const [requeueConfirm, setRequeueConfirm] = useState(false)

  return (
    <>
      <Sheet open onOpenChange={(open) => { if (!open) onClose() }}>
        <SheetContent className="flex flex-col gap-0 w-full sm:max-w-3xl overflow-y-auto">
          <SheetHeader className="pb-4">
            <SheetTitle className="font-mono text-sm">{queue.name}</SheetTitle>
            <SheetDescription>
              {queue.messages} message{queue.messages !== 1 ? "s" : ""} in queue ·{" "}
              {queue.consumers} consumer{queue.consumers !== 1 ? "s" : ""}
            </SheetDescription>
          </SheetHeader>

          <div className="flex flex-col gap-4 py-4 mx-6">
            {requeue.data && !requeue.data.requeued && requeue.data.lost && (
              <LostMessageAlert
                result={requeue.data}
                onDismiss={() => requeue.reset()}
              />
            )}
            {isLoading ? (
              <div className="flex flex-col gap-3 animate-pulse">
                {Array.from({ length: 3 }).map((_, i) => (
                  <div key={i} className="rounded-lg bg-muted h-24" />
                ))}
              </div>
            ) : !messages || messages.length === 0 ? (
              <div className="flex flex-col items-center justify-center gap-2 py-12 text-center text-muted-foreground">
                <IconMailExclamation className="size-8 opacity-40" />
                <p className="text-sm">No messages to display</p>
              </div>
            ) : (
              <div className="flex flex-col gap-3">
                {messages.map((msg: DLQMessage, i: number) => (
                  <MessageRow key={i} index={i} msg={msg} />
                ))}
              </div>
            )}
          </div>

          <SheetFooter className="pt-4 mt-auto">
            <Button
              variant="outline"
              size="sm"
              onClick={() => refetch()}
              disabled={isLoading}
            >
              <IconRefresh className={`size-4 ${isLoading ? "animate-spin" : ""}`} />
              Refresh
            </Button>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => setRequeueConfirm(true)}
              disabled={requeue.isPending || queue.messages === 0}
            >
              <IconRotateClockwise className="size-4" />
              Requeue next
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      <AlertDialog open={requeueConfirm} onOpenChange={setRequeueConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Requeue next message?</AlertDialogTitle>
            <AlertDialogDescription>
              The next message in <span className="font-mono">{queue.name}</span> will be
              consumed and republished to its original exchange. This operation cannot be
              undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setRequeueConfirm(false)
                requeue.mutate(queue.name)
              }}
            >
              Requeue
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function LostMessageAlert({
  result,
  onDismiss,
}: {
  result: RequeueResult
  onDismiss: () => void
}) {
  const lost = result.lost!

  let prettyBody = lost.body
  try {
    prettyBody = JSON.stringify(JSON.parse(lost.body), null, 2)
  } catch {
    // not JSON — show as-is
  }

  return (
    <div className="rounded-lg border border-destructive/50 bg-destructive/5 text-sm">
      <div className="flex items-start justify-between gap-3 px-4 py-3">
        <div className="flex items-start gap-2">
          <IconAlertTriangle className="size-4 shrink-0 mt-0.5 text-destructive" />
          <div className="flex flex-col gap-1 min-w-0">
            <p className="font-medium text-destructive">
              Message lost — republish failed
            </p>
            <p className="text-xs text-muted-foreground">
              This message was removed from the DLQ but could not be republished.
              {result.error ? ` ${result.error}` : ""} Save the body below and
              republish it manually.
            </p>
            <div className="flex items-center gap-2 flex-wrap mt-1">
              <code className="text-xs font-mono bg-muted px-1.5 py-0.5 rounded">
                {lost.routing_key || "(no routing key)"}
              </code>
              {lost.exchange && (
                <span className="text-xs text-muted-foreground">
                  exchange: <span className="font-mono">{lost.exchange}</span>
                </span>
              )}
            </div>
          </div>
        </div>
        <button
          onClick={onDismiss}
          className="text-xs text-muted-foreground hover:text-foreground shrink-0"
        >
          dismiss
        </button>
      </div>
      <div className="border-t border-destructive/50 px-4 py-3">
        <pre className="text-xs font-mono whitespace-pre-wrap break-all text-muted-foreground max-h-64 overflow-y-auto">
          {prettyBody || "(empty body)"}
        </pre>
      </div>
    </div>
  )
}

function MessageRow({ index, msg }: { index: number; msg: DLQMessage }) {
  const [expanded, setExpanded] = useState(false)

  let prettyBody = msg.body
  try {
    prettyBody = JSON.stringify(JSON.parse(msg.body), null, 2)
  } catch {
    // not JSON — show as-is
  }

  return (
    <div className="rounded-lg border bg-card text-sm">
      <div className="flex items-start justify-between gap-3 px-4 py-3">
        <div className="flex flex-col gap-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs text-muted-foreground">#{index + 1}</span>
            <code className="text-xs font-mono bg-muted px-1.5 py-0.5 rounded">
              {msg.routing_key || "(no routing key)"}
            </code>
            {msg.redelivered && (
              <Badge variant="secondary" className="text-[10px] h-4">redelivered</Badge>
            )}
          </div>
          {msg.exchange && (
            <span className="text-xs text-muted-foreground">
              exchange: <span className="font-mono">{msg.exchange}</span>
            </span>
          )}
        </div>
        <button
          onClick={() => setExpanded((v) => !v)}
          className="text-xs text-muted-foreground hover:text-foreground shrink-0"
        >
          {expanded ? "hide" : "body"}
        </button>
      </div>

      {expanded && (
        <div className="border-t px-4 py-3">
          <pre className="text-xs font-mono whitespace-pre-wrap break-all text-muted-foreground max-h-64 overflow-y-auto">
            {prettyBody || "(empty body)"}
          </pre>
        </div>
      )}
    </div>
  )
}
