import { useTranslation } from "react-i18next"
import { Link } from "@tanstack/react-router"
import { IconBell } from "@tabler/icons-react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  useNotificationCount,
  useNotifications,
  useMarkNotificationRead,
  useMarkAllNotificationsRead,
} from "@/features/notification/hooks"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/ui"
import { timeAgo } from "@/lib/format"

interface Props {
  organizationId?: string
}

export function NotificationBell({ organizationId }: Props) {
  const { t } = useTranslation()

  const { data: countData } = useNotificationCount()
  const { data: notifData } = useNotifications(undefined, undefined, 5)
  const { mutate: markRead } = useMarkNotificationRead()
  const { mutate: markAllRead } = useMarkAllNotificationsRead()

  const unreadCount = countData?.data?.unread ?? 0
  const notifications = notifData?.data ?? []

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={
          unreadCount > 0
            ? `${unreadCount} unread notifications`
            : "Notifications"
        }
        className="relative flex size-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      >
        <IconBell className="size-4" />
        {unreadCount > 0 && (
          <span className="absolute -top-0.5 -right-0.5 flex size-4 items-center justify-center rounded-full bg-destructive text-[10px] font-bold text-white">
            {unreadCount > 9 ? "9+" : unreadCount}
          </span>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent side="bottom" align="end" className="w-80">
        <div className="flex items-center justify-between gap-2 px-2 py-1">
          <span className="text-xs font-semibold">
            {t("notifications.title")}
          </span>
          {unreadCount > 0 && (
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-1.5 text-[10px]"
              onClick={(e) => {
                e.preventDefault()
                markAllRead()
              }}
            >
              {t("notifications.markAllRead")}
            </Button>
          )}
        </div>
        <DropdownMenuSeparator />
        {notifications.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {t("notifications.empty")}
          </p>
        ) : (
          notifications.map((n) => {
            const isUnread = !n.read_at
            return (
              <DropdownMenuItem
                key={n.id}
                className={cn(
                  "mb-1 flex flex-col items-start gap-0.5 py-2",
                  isUnread && "bg-primary/5"
                )}
                onClick={() => isUnread && markRead(n.id)}
              >
                <div className="flex w-full items-center justify-between gap-1">
                  <span
                    className={cn(
                      "text-xs",
                      isUnread ? "font-semibold" : "font-medium"
                    )}
                  >
                    {n.subject}
                  </span>
                  <div className="flex shrink-0 items-center gap-1.5">
                    {isUnread && (
                      <span className="size-1.5 rounded-full bg-primary" />
                    )}
                    <span className="text-[10px] text-muted-foreground">
                      {timeAgo(n.created_at)}
                    </span>
                  </div>
                </div>
                <span className="line-clamp-2 text-xs text-muted-foreground">
                  {n.body}
                </span>
              </DropdownMenuItem>
            )
          })
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          render={
            <Link
              to="/notifications"
              search={{ organization_id: organizationId }}
            />
          }
          className="justify-center text-xs font-medium"
        >
          {t("notifications.viewAll")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
