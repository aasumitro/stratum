import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconChevronDown, IconChevronUp, IconLock } from "@tabler/icons-react"
import { Switch } from "@/components/ui/switch"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  useNotificationPreferences,
  useUpdateNotificationPreference,
} from "@/features/notification/hooks"
import { useOrganizations } from "@/features/organization/hooks"
import {
  CATEGORIES,
  CATEGORY_ICON,
  eventTypesInCategory,
  LOCKED_OWNER_CHANNEL,
  LOCKED_OWNER_EVENT_TYPE,
  type NotificationCategory,
} from "@/features/notification/notification-categories"

function label(eventType: string): string {
  return eventType.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())
}

// Category × channel matrix with per-event expansion (progressive
// disclosure keeps the backend's 18 raw keys reachable, not front-and-center).
// The owner payment-failure email lock is enforced here only — a UI-only
// guard, same convention as the rest of this app's RBAC (the frontend only
// hides/disables; the backend is what actually enforces access). The
// backend's PATCH .../preferences has no equivalent server-side check.
export function NotificationPreferencesPanel() {
  const { t } = useTranslation()
  const { data, isLoading } = useNotificationPreferences()
  const { mutate } = useUpdateNotificationPreference()
  const { data: organizationsData } = useOrganizations()
  const isOwnerAnywhere = (organizationsData?.data ?? []).some(
    (o) => o.role === "owner"
  )
  const [expanded, setExpanded] = useState<NotificationCategory | null>(null)

  const prefs = data?.data ?? []

  function isEnabled(channel: string, eventType: string): boolean {
    const row = prefs.find(
      (p) => p.channel === channel && p.event_type === eventType
    )
    return row ? row.enabled : true
  }

  function isLocked(channel: string, eventType: string): boolean {
    return (
      isOwnerAnywhere &&
      channel === LOCKED_OWNER_CHANNEL &&
      eventType === LOCKED_OWNER_EVENT_TYPE
    )
  }

  function toggle(channel: string, eventType: string, enabled: boolean) {
    if (isLocked(channel, eventType)) return
    mutate(
      { channel, event_type: eventType, enabled },
      {
        onSuccess: () => toast.success(t("notifications.preferences.updated")),
        onError: () => toast.error(t("notifications.preferences.updateFailed")),
      }
    )
  }

  function categoryEnabled(category: NotificationCategory, channel: string) {
    const events = eventTypesInCategory(category)
    return events.length > 0 && events.every((e) => isEnabled(channel, e))
  }

  function toggleCategory(
    category: NotificationCategory,
    channel: string,
    enabled: boolean
  ) {
    for (const eventType of eventTypesInCategory(category)) {
      toggle(channel, eventType, enabled)
    }
  }

  if (isLoading) {
    return (
      <div className="flex flex-col gap-3">
        {[1, 2, 3, 4].map((i) => (
          <Skeleton key={i} className="h-14 w-full" />
        ))}
      </div>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("notifications.preferences.title")}</CardTitle>
        <CardDescription>
          {t("notifications.preferences.description")}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-0 p-0">
        <div className="grid grid-cols-[1fr_5rem_5rem] items-center gap-2 border-b bg-muted/40 px-4 py-2.5 text-xs font-medium text-muted-foreground">
          <span>{t("notifications.preferences.category")}</span>
          <span className="text-center">
            {t("notifications.preferences.channelInApp")}
          </span>
          <span className="text-center">
            {t("notifications.preferences.channelEmail")}
          </span>
        </div>
        {CATEGORIES.map((category) => {
          const events = eventTypesInCategory(category)
          const hasEvents = events.length > 0
          const isOpen = expanded === category
          const Icon = CATEGORY_ICON[category]
          return (
            <div key={category} className="border-b last:border-b-0">
              <div className="grid grid-cols-[1fr_5rem_5rem] items-center gap-2 px-4 py-3">
                <button
                  type="button"
                  disabled={!hasEvents}
                  onClick={() => setExpanded(isOpen ? null : category)}
                  className="flex items-center gap-2.5 text-left text-sm font-medium disabled:cursor-default disabled:text-muted-foreground"
                >
                  <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted">
                    <Icon className="size-4 text-muted-foreground" />
                  </span>
                  {t(`notifications.tabs.${category}`)}
                  {hasEvents &&
                    (isOpen ? (
                      <IconChevronUp className="size-3.5 text-muted-foreground" />
                    ) : (
                      <IconChevronDown className="size-3.5 text-muted-foreground" />
                    ))}
                </button>
                <div className="flex justify-center">
                  <Switch
                    checked={categoryEnabled(category, "in_app")}
                    disabled={!hasEvents}
                    onCheckedChange={(v) =>
                      toggleCategory(category, "in_app", v)
                    }
                    aria-label={`${category} in-app`}
                  />
                </div>
                <div className="flex justify-center">
                  <Switch
                    checked={categoryEnabled(category, "email")}
                    disabled={!hasEvents}
                    onCheckedChange={(v) =>
                      toggleCategory(category, "email", v)
                    }
                    aria-label={`${category} email`}
                  />
                </div>
              </div>

              {!hasEvents && (
                <p className="px-4 pb-3 text-xs text-muted-foreground">
                  {t("notifications.preferences.noConfigurableEvents")}
                </p>
              )}

              {isOpen && hasEvents && (
                <div className="flex flex-col gap-2 bg-muted/20 px-4 pb-3">
                  {events.map((eventType) => {
                    const locked = isLocked("email", eventType)
                    return (
                      <div
                        key={eventType}
                        className="grid grid-cols-[1fr_5rem_5rem] items-center gap-2 py-1"
                      >
                        {/* pl matches the category row's icon (size-8) + gap-2.5 above, so nested event labels visually align under the category text instead of the icon */}
                        <span className="pl-[2.625rem] text-xs text-muted-foreground">
                          {label(eventType)}
                        </span>
                        <div className="flex justify-center">
                          <Switch
                            checked={isEnabled("in_app", eventType)}
                            onCheckedChange={(v) =>
                              toggle("in_app", eventType, v)
                            }
                            aria-label={`${label(eventType)} in-app`}
                          />
                        </div>
                        <div className="flex justify-center">
                          {locked ? (
                            <IconLock
                              className="size-3.5 text-muted-foreground"
                              aria-label={t(
                                "notifications.preferences.ownerLocked"
                              )}
                            />
                          ) : (
                            <Switch
                              checked={isEnabled("email", eventType)}
                              onCheckedChange={(v) =>
                                toggle("email", eventType, v)
                              }
                              aria-label={`${label(eventType)} email`}
                            />
                          )}
                        </div>
                      </div>
                    )
                  })}
                  {events.includes(LOCKED_OWNER_EVENT_TYPE) &&
                    isOwnerAnywhere && (
                      <p className="pt-1 text-xs text-muted-foreground">
                        {t(
                          "notifications.preferences.paymentFailureLockedNote"
                        )}
                      </p>
                    )}
                </div>
              )}
            </div>
          )
        })}
      </CardContent>
    </Card>
  )
}
