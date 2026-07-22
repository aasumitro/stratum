import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import {
  IconBroadcast,
  IconCalendar,
  IconCheck,
  IconInfoCircle,
  IconSend,
  IconUsers,
} from "@tabler/icons-react"
import type { OrganizationSummary, UserResult } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useBroadcastHistory, useCountRecipients, useSendBroadcast } from "@/hooks/use-broadcast"
import { useOrganizations } from "@/hooks/use-organization-ops"
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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/ui"
import { buildTarget, formatTarget, UserSearchDropdown, type TargetType } from "./user-search-dropdown"

export function BroadcastPage() {
  const { projectId } = useParams({ from: "/studio/$projectId/broadcast" })

  const [title, setTitle] = useState("")
  const [body, setBody] = useState("")
  const [targetType, setTargetType] = useState<TargetType>("all")
  const [selectedOrganization, setSelectedOrganization] = useState<OrganizationSummary | null>(null)
  const [organizationSearch, setOrganizationSearch] = useState("")
  const [selectedUser, setSelectedUser] = useState<UserResult | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)

  const { data: allOrganizations } = useOrganizations(projectId, "active", 0)
  const organizations = organizationSearch.trim()
    ? (allOrganizations ?? []).filter(
        (o) =>
          o.name.toLowerCase().includes(organizationSearch.toLowerCase()) ||
          o.slug.toLowerCase().includes(organizationSearch.toLowerCase()),
      )
    : (allOrganizations ?? [])

  const target = buildTarget(targetType, selectedOrganization, selectedUser)
  const isTargetReady = !!target && (targetType !== "user" || !!selectedUser) && (targetType !== "organization" || !!selectedOrganization)

  const { data: recipientCount } = useCountRecipients(projectId, isTargetReady ? target : "")
  const { data: history, isLoading: historyLoading, error: historyError, refetch } = useBroadcastHistory(projectId)
  const send = useSendBroadcast(projectId)

  const canSend = title.trim() && body.trim() && isTargetReady && !send.isPending

  function handleConfirm() {
    if (!canSend) return
    send.mutate({ title: title.trim(), body: body.trim(), target }, {
      onSuccess: () => {
        setTitle("")
        setBody("")
        setTargetType("all")
        setSelectedOrganization(null)
        setOrganizationSearch("")
        setSelectedUser(null)
        setConfirmOpen(false)
      },
      onError: () => setConfirmOpen(false),
    })
  }

  return (
    <div className="flex flex-col min-h-full">
      <header className="flex items-center gap-3 px-6 py-4 border-b">
        <IconBroadcast className="size-4 text-muted-foreground" />
        <div className="flex flex-col gap-0.5">
          <h1 className="font-semibold text-sm">Broadcast</h1>
          <p className="text-xs text-muted-foreground">
            Send in-app notifications directly to users
          </p>
        </div>
      </header>

      <div className="p-6 space-y-5">
      {/* In-app only notice */}
      <div className="flex items-start gap-2.5 rounded-lg border bg-muted/40 px-4 py-3 text-sm text-muted-foreground">
        <IconInfoCircle className="size-4 mt-0.5 flex-shrink-0" />
        <span>
          Broadcasts are delivered as <strong className="text-foreground">in-app</strong> notifications.
          Email and push channels require a broadcast handler in the API worker.
        </span>
      </div>

      {/* Compose card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Compose</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5">
          {/* Title */}
          <div className="space-y-1.5">
            <Label htmlFor="bc-title">Title</Label>
            <Input
              id="bc-title"
              placeholder="e.g. Scheduled maintenance this Sunday"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </div>

          {/* Body */}
          <div className="space-y-1.5">
            <Label htmlFor="bc-body">Message</Label>
            <Textarea
              id="bc-body"
              rows={4}
              placeholder="Write your message here…"
              value={body}
              onChange={(e) => setBody(e.target.value)}
            />
          </div>

          {/* Target */}
          <div className="space-y-1.5">
            <Label htmlFor="bc-target">Target</Label>
            <select
              id="bc-target"
              value={targetType}
              onChange={(e) => {
                setTargetType(e.target.value as TargetType)
                setSelectedOrganization(null)
                setOrganizationSearch("")
                setSelectedUser(null)
              }}
              className={cn(
                "w-full h-9 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm",
                "focus:outline-none focus:ring-1 focus:ring-ring transition-colors",
              )}
            >
              <option value="all">All users</option>
              <option value="organization">Organization members</option>
              <option value="user">Specific user</option>
            </select>

            {targetType === "organization" && (
              <div className="pt-1 space-y-1.5">
                <Input
                  placeholder="Search organization by name…"
                  value={organizationSearch}
                  onChange={(e) => {
                    setOrganizationSearch(e.target.value)
                    setSelectedOrganization(null)
                  }}
                />
                {selectedOrganization && (
                  <div className="flex items-center gap-2 text-sm text-muted-foreground">
                    <IconCheck className="size-3.5 text-green-500" />
                    {selectedOrganization.name} ({selectedOrganization.slug})
                  </div>
                )}
                {!selectedOrganization && organizations && organizations.length > 0 && (
                  <div className="border rounded-md divide-y max-h-48 overflow-y-auto bg-popover shadow-md">
                    {organizations.map((org) => (
                      <button
                        key={org.id}
                        type="button"
                        className="w-full text-left px-3 py-2 text-sm hover:bg-accent transition-colors"
                        onClick={() => {
                          setSelectedOrganization(org)
                          setOrganizationSearch("")
                        }}
                      >
                        <div className="font-medium">{org.name}</div>
                        <div className="text-muted-foreground text-xs font-mono">{org.slug} · {org.status}</div>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )}

            {targetType === "user" && (
              <div className="pt-1">
                <UserSearchDropdown
                  projectId={projectId}
                  selected={selectedUser}
                  onSelect={setSelectedUser}
                />
              </div>
            )}
          </div>

          {/* Recipient count preview */}
          {isTargetReady && recipientCount !== undefined && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <IconUsers className="size-4" />
              <span>
                <strong className="text-foreground">{recipientCount}</strong>{" "}
                recipient{recipientCount === 1 ? "" : "s"} will receive this broadcast
              </span>
            </div>
          )}

          <Button
            onClick={() => setConfirmOpen(true)}
            disabled={!canSend}
            className="gap-2"
          >
            <IconSend className="size-4" />
            Send Broadcast
          </Button>
        </CardContent>
      </Card>

      {/* History */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Sent History</CardTitle>
        </CardHeader>
        <CardContent>
          {historyLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-9 w-full" />
              ))}
            </div>
          ) : historyError ? (
            <div className="flex flex-col items-center gap-2 py-8 text-muted-foreground text-sm">
              <span>Failed to load history</span>
              <Button variant="outline" size="sm" onClick={() => refetch()}>
                Try again
              </Button>
            </div>
          ) : !history || history.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-10 text-muted-foreground">
              <IconBroadcast className="size-8 opacity-30" />
              <span className="text-sm">No broadcasts sent yet</span>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Title</TableHead>
                  <TableHead>Target</TableHead>
                  <TableHead className="text-right">Recipients</TableHead>
                  <TableHead>Sent At</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {history.map((log) => (
                  <TableRow key={log.id}>
                    <TableCell className="font-medium max-w-[200px] truncate">{log.title}</TableCell>
                    <TableCell>
                      <Badge variant="secondary" className="font-mono text-xs">
                        {formatTarget(log.target, null, null)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      <span className="flex items-center justify-end gap-1.5 text-sm">
                        <IconUsers className="size-3.5 text-muted-foreground" />
                        {log.recipient_count}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
                        <IconCalendar className="size-3.5" />
                        {new Date(log.sent_at).toLocaleString()}
                      </span>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Confirm send dialog */}
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Send broadcast?</AlertDialogTitle>
            <AlertDialogDescription className="space-y-2 text-sm">
              This will send an in-app notification to{" "}
              <strong>{recipientCount ?? "…"}</strong>{" "}
              user{recipientCount === 1 ? "" : "s"}.
              <span className="block mt-2 rounded border bg-muted/50 px-3 py-2">
                <span className="block font-medium text-foreground">{title}</span>
                <span className="block text-muted-foreground">{body}</span>
              </span>
              <span className="block mt-1">
                Target:{" "}
                <code className="text-xs bg-muted px-1 py-0.5 rounded">
                  {formatTarget(target, selectedOrganization, selectedUser)}
                </code>
              </span>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleConfirm} disabled={send.isPending}>
              {send.isPending ? "Sending…" : "Send"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      </div>
    </div>
  )
}
