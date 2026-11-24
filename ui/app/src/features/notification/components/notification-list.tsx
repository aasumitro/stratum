import { useMemo, useState } from "react"
import type { Notification } from "@/types/notification"
import { useTranslation } from "react-i18next"
import { useNavigate } from "@tanstack/react-router"
import { IconArrowRight, IconBell, IconCircleCheck } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { EmptyState } from "@/components/shared/empty-state"
import {
  chunk,
  collapseRuns,
  isRunOpen,
  toggleRun as toggledRuns,
  type NotificationRun,
} from "@/features/notification/collapse"
import {
  useNotifications,
  useMarkNotificationRead,
  useMarkNotificationsRead,
} from "@/features/notification/hooks"
import { useCursorAccumulator } from "@/lib/api/use-cursor-accumulator"
import {
  CATEGORY_ICON,
  CHANNEL_CATEGORY,
  type NotificationCategory,
} from "@/features/notification/notification-categories"
import { cn } from "@/lib/ui"
import { timeAgo } from "@/lib/format"
import { notificationDisplayText } from "@/features/notification/notification-text"

type DateGroupKey = "today" | "yesterday" | "week" | "earlier"
const DATE_GROUP_ORDER: DateGroupKey[] = [
  "today",
  "yesterday",
  "week",
  "earlier",
]

function dateGroupKey(dateStr: string, now: Date): DateGroupKey {
  const startOfDay = (d: Date) =>
    new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const diffDays = Math.floor(
    (startOfDay(now) - startOfDay(new Date(dateStr))) / 86_400_000
  )
  if (diffDays <= 0) return "today"
  if (diffDays === 1) return "yesterday"
  if (diffDays < 7) return "week"
  return "earlier"
}

// Turns the flat cursor timeline into scannable Today/Yesterday/This
// week/Earlier sections — items already arrive newest-first, so grouping is
// a single pass, no re-sort needed.
function groupByDate(items: Notification[]) {
  const now = new Date()
  const buckets = new Map<DateGroupKey, Notification[]>()
  for (const n of items) {
    const key = dateGroupKey(n.created_at, now)
    const bucket = buckets.get(key)
    if (bucket) bucket.push(n)
    else buckets.set(key, [n])
  }
  return DATE_GROUP_ORDER.filter((k) => buckets.has(k)).map((key) => ({
    key,
    items: buckets.get(key)!,
  }))
}

// Deep-link target for a notification's row action, by category. Billing
// failures route straight to Billing ("Fix →"), where invoices live;
// everything else lands on the module's home for that organization.
// Invites are a special case — the recipient isn't a member of that
// organization yet, so its own pages are off-limits; the picker page is
// where the accept/decline card actually lives.
function actionPathFor(n: Notification): string | undefined {
  if (n.channel === "invite") return "/organizations"
  if (!n.organization_id) return undefined
  const category = CHANNEL_CATEGORY[n.channel]
  switch (category) {
    case "billing":
      return `/organization/${n.organization_id}/billing`
    case "members":
      return `/organization/${n.organization_id}/members`
    case "organization":
      return `/organization/${n.organization_id}`
    default:
      return undefined
  }
}

interface Props {
  organizationId?: string
  organizationNames: Record<string, string>
  category?: NotificationCategory
  // Server-side search over subject and body; "" means none.
  search?: string
}

export function NotificationList({
  organizationId,
  organizationNames,
  category,
  search,
}: Props) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  // Category tabs are a frontend grouping over the backend's flat
  // `channel` column — filtered client-side below, not via the API, so the
  // cursor stays anchored to the full unfiltered timeline.
  const { data, isLoading, isFetching } = useNotifications(
    organizationId,
    cursor,
    20,
    undefined,
    search || undefined
  )
  const { items: allItems, nextCursor } = useCursorAccumulator<Notification>(
    data,
    cursor
  )
  const items = useMemo(() => {
    const safeItems = allItems || []
    return category
      ? safeItems.filter((n) => CHANNEL_CATEGORY[n.channel] === category)
      : safeItems
  }, [allItems, category])
  const groups = useMemo(() => groupByDate(items), [items])
  const { mutate: markRead } = useMarkNotificationRead(organizationId)
  const { mutate: markManyRead } = useMarkNotificationsRead(organizationId)

  if (isLoading && items.length === 0) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="flex flex-col gap-1 rounded-lg border p-3">
            <Skeleton className="h-4 w-48" />
            <Skeleton className="h-3.5 w-full" />
          </div>
        ))}
      </div>
    )
  }

  if (items.length === 0) {
    return (
      <EmptyState
        icon={IconCircleCheck}
        title={t("notifications.caughtUp")}
        description={t("notifications.empty")}
      />
    )
  }

  function toggleRun(run: NotificationRun) {
    setExpanded((prev) => toggledRuns(run, prev))
  }

  function markRunRead(n: Notification, run?: NotificationRun) {
    const unread = [n, ...(run?.rest ?? [])]
      .filter((x) => !x.read_at)
      .map((x) => x.id)
    if (unread.length === 1) markRead(unread[0])
    // The API takes at most 100 ids a request.
    else for (const ids of chunk(unread, 100)) markManyRead({ ids })
  }

  // One notification row. A run's lead also carries the folded count and the
  // toggle; reading it reads the whole run.
  function renderRow(n: Notification, run?: NotificationRun) {
    const isUnread = !n.read_at || (run?.rest.some((x) => !x.read_at) ?? false)
    const actionPath = actionPathFor(n)
    const rowCategory = CHANNEL_CATEGORY[n.channel]
    const Icon = rowCategory ? CATEGORY_ICON[rowCategory] : IconBell
    const { subject, body } = notificationDisplayText(
      n,
      t,
      i18n.exists.bind(i18n)
    )
    return (
      <div
        key={n.id}
        className={cn(
          "flex items-start gap-3 px-4 py-3.5 transition-colors hover:bg-muted/40",
          isUnread && "bg-muted/25"
        )}
      >
        <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-muted">
          <Icon className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0 flex-1">
          <button
            type="button"
            className="w-full text-left"
            onClick={() => markRunRead(n, run)}
          >
            <div className="flex w-full items-center gap-2">
              <span
                className={cn(
                  "text-sm",
                  isUnread ? "font-semibold" : "font-medium"
                )}
              >
                {subject}
              </span>
              {isUnread && (
                <span
                  className="size-1.5 shrink-0 rounded-full bg-foreground"
                  aria-label={t("notifications.unread")}
                />
              )}
              <span className="ml-auto shrink-0 text-xs text-muted-foreground">
                {timeAgo(n.created_at)}
              </span>
            </div>
            <p className="line-clamp-2 text-xs text-muted-foreground">{body}</p>
            {organizationNames[n.organization_id] && (
              <span className="text-xs text-muted-foreground/70">
                {organizationNames[n.organization_id]}
              </span>
            )}
          </button>
          {run && run.rest.length > 0 && (
            <button
              type="button"
              aria-expanded={isRunOpen(run, expanded)}
              aria-controls={`notification-run-${run.lead.id}`}
              className="mt-1 text-xs font-medium text-foreground underline-offset-4 hover:underline"
              onClick={() => toggleRun(run)}
            >
              {isRunOpen(run, expanded)
                ? t("notifications.showLess")
                : t("notifications.moreLikeThis", {
                    count: run.rest.length,
                  })}
            </button>
          )}
        </div>
        {actionPath && (
          <Button
            variant="ghost"
            size="sm"
            className="shrink-0"
            onClick={() => {
              markRunRead(n, run)
              void navigate({ to: actionPath })
            }}
          >
            {t("common.view")}
            <IconArrowRight data-icon="inline-end" />
          </Button>
        )}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <Card
        size="sm"
        className="animate-in gap-0 py-0 duration-300 fade-in slide-in-from-bottom-1 motion-reduce:animate-none"
      >
        {groups.map(({ key, items: groupItems }) => (
          <div key={key}>
            <p className="border-b bg-muted/40 px-4 py-2 text-xs font-medium tracking-wide text-muted-foreground uppercase first:rounded-t-[inherit]">
              {t(`notifications.dateGroups.${key}`)}
            </p>
            <div className="divide-y">
              {collapseRuns(groupItems).map((run) => (
                <div key={run.lead.id}>
                  {renderRow(run.lead, run)}
                  {isRunOpen(run, expanded) && run.rest.length > 0 && (
                    <div
                      id={`notification-run-${run.lead.id}`}
                      className="divide-y border-t bg-muted/10 pl-6"
                    >
                      {run.rest.map((n) => (
                        <div key={n.id}>{renderRow(n)}</div>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        ))}
      </Card>

      {nextCursor && (
        <Button
          variant="outline"
          size="sm"
          className="w-full"
          disabled={isFetching}
          onClick={() => setCursor(nextCursor)}
        >
          {isFetching ? t("common.loading") : t("common.loadMore")}
        </Button>
      )}
    </div>
  )
}
