import { useSearch } from "@tanstack/react-router"
import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { NotificationList } from "@/features/notification/components/notification-list"
import { NotificationPreferencesPanel } from "@/features/notification/components/notification-preferences-panel"
import {
  useNotificationCount,
  useMarkAllNotificationsRead,
} from "@/features/notification/hooks"
import { useOrganizations } from "@/features/organization/hooks/use-organization"
import {
  FEED_CATEGORIES,
  type NotificationCategory,
} from "@/features/notification/notification-categories"

export function NotificationPage() {
  const { organization_id: initialOrganizationId } = useSearch({
    from: "/_protected/notifications",
  })
  const { t } = useTranslation()
  const [organizationId, setOrganizationId] = useState(
    initialOrganizationId ?? ""
  )
  const [tab, setTab] = useState<"all" | NotificationCategory>("all")
  const [view, setView] = useState<"feed" | "prefs">("feed")

  const { data: organizationsData } = useOrganizations()
  const organizations = organizationsData?.data ?? []
  const organizationNames = useMemo(
    () =>
      Object.fromEntries(
        (organizationsData?.data ?? []).map((o) => [o.id, o.name])
      ),
    [organizationsData]
  )

  const { data: countData } = useNotificationCount(organizationId || undefined)
  const unreadCount = countData?.data?.unread ?? 0
  const { mutate: markAllRead, isPending: isMarkingAll } =
    useMarkAllNotificationsRead(organizationId || undefined)

  const organizationSelectItems = [
    { value: "all", label: t("nav.allOrganizations") },
    ...organizations.map((o) => ({ value: o.id, label: o.name })),
  ]

  return (
    // w-full is load-bearing: this is a flex item inside protected-layout's
    // <main>, and margin-auto centering overrides align-items:stretch, so
    // without it the container shrinks to its content's width (visibly
    // narrower whenever a filter tab renders the empty state) instead of
    // holding a stable max-w-3xl.
    <div className="mx-auto flex w-full max-w-3xl min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-3 rounded-xl border bg-muted/40 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <h1 className="text-xl font-semibold">
              {t("notifications.title")}
            </h1>
            {unreadCount > 0 && (
              <Badge variant="destructive">
                {t("notifications.unreadCount", { count: unreadCount })}
              </Badge>
            )}
          </div>
          <Tabs
            value={view}
            onValueChange={(v) => setView(v as "feed" | "prefs")}
          >
            <TabsList>
              <TabsTrigger value="feed">
                {t("notifications.feedTab")}
              </TabsTrigger>
              <TabsTrigger value="prefs">
                {t("notifications.preferencesButton")}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        </div>

        {view === "feed" && (
          <div className="flex flex-wrap items-center justify-between gap-3">
            <Tabs
              value={tab}
              onValueChange={(v) => setTab(v as "all" | NotificationCategory)}
            >
              <TabsList>
                <TabsTrigger value="all">
                  {t("notifications.tabs.all")}
                </TabsTrigger>
                {FEED_CATEGORIES.map((c) => (
                  <TabsTrigger key={c} value={c}>
                    {t(`notifications.tabs.${c}`)}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>

            <div className="flex items-center gap-2">
              <Select
                items={organizationSelectItems}
                value={organizationId || "all"}
                onValueChange={(v) =>
                  setOrganizationId(!v || v === "all" ? "" : v)
                }
              >
                <SelectTrigger size="sm" className="w-44">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">
                      {t("nav.allOrganizations")}
                    </SelectItem>
                    {organizations.map((o) => (
                      <SelectItem key={o.id} value={o.id}>
                        {o.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              {unreadCount > 0 && (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={isMarkingAll}
                  onClick={() =>
                    markAllRead(undefined, {
                      onSuccess: () =>
                        toast.success(t("notifications.markAllReadSuccess")),
                    })
                  }
                >
                  {t("notifications.markAllRead")}
                </Button>
              )}
            </div>
          </div>
        )}
      </div>

      {view === "feed" ? (
        // key resets pagination when the org filter changes; the category
        // tab doesn't need it — it's a client-side filter over the same
        // fetched timeline, not a query param, so remounting per tab would
        // just discard already-loaded pages for no reason.
        <NotificationList
          key={organizationId || "all"}
          organizationId={organizationId || undefined}
          organizationNames={organizationNames}
          category={tab === "all" ? undefined : tab}
        />
      ) : (
        <NotificationPreferencesPanel />
      )}
    </div>
  )
}
