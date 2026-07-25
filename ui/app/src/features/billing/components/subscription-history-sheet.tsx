import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconRobot, IconUser, IconWebhook } from "@tabler/icons-react"
import { SideDrawer } from "@/components/shared/side-drawer"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useBillingHistory } from "@/features/billing/hooks"
import { capitalize, formatMoney } from "@/lib/format"
import type { SubscriptionHistory } from "@/types/billing"

const PAGE_SIZE = 8

function formatDate(s: string) {
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

// One consistent badge for every actor kind — a real user gets a generic
// person icon rather than initials, matching the system/webhook badges.
function ActorBadge({
  name,
  kind,
}: {
  name: string
  kind: "user" | "system" | "webhook"
}) {
  const Icon =
    kind === "webhook" ? IconWebhook : kind === "system" ? IconRobot : IconUser
  return (
    <span className="inline-flex w-fit items-center gap-1.5 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
      <Icon className="size-3" />
      {name}
    </span>
  )
}

function logLine(
  h: SubscriptionHistory,
  t: (key: string, opts?: Record<string, unknown>) => string
) {
  switch (h.action) {
    case "upgrade":
    case "downgrade":
      return t("billing.history.logChange", {
        fromPlan: h.from_plan ? capitalize(h.from_plan) : "—",
        toPlan: h.to_plan ? capitalize(h.to_plan) : "—",
      })
    case "trial":
    case "activate":
      return t("billing.history.logStart", {
        toPlan: h.to_plan ? capitalize(h.to_plan) : "—",
      })
    case "cancel":
      return t("billing.history.logCancel")
    case "resume":
      return t("billing.history.logResume")
    case "expire":
      return t("billing.history.logExpire")
    case "extend":
      return t("billing.history.logExtend")
  }
}

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function SubscriptionHistorySheet({
  organizationId,
  open,
  onOpenChange,
}: Props) {
  const { t } = useTranslation()
  const { data, isLoading } = useBillingHistory(organizationId)
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)
  const history = data?.data ?? []
  const visible = history.slice(0, visibleCount)

  return (
    <SideDrawer
      open={open}
      onOpenChange={onOpenChange}
      title={t("billing.history.title")}
      description={t("billing.history.description")}
      className="sm:max-w-md"
    >
      {isLoading ? (
        <div className="flex flex-col gap-3 py-4">
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      ) : !history.length ? (
        <p className="py-4 text-sm text-muted-foreground">
          {t("billing.history.noHistory")}
        </p>
      ) : (
        <div className="flex flex-col gap-3 py-4">
          <ul className="flex flex-col gap-2">
            {visible.map((h) => (
              <li
                key={h.id}
                className="flex flex-col gap-1.5 rounded-lg border p-3"
              >
                <ActorBadge name={h.changed_by_name} kind={h.changed_by_kind} />
                <p className="text-sm font-medium">{logLine(h, t)}</p>
                <p className="text-xs text-muted-foreground">
                  {h.amount_cents > 0
                    ? `${formatMoney(h.amount_cents, h.currency)} · `
                    : ""}
                  {formatDate(h.changed_at)}
                </p>
              </li>
            ))}
          </ul>
          {visibleCount < history.length && (
            <Button
              variant="outline"
              className="w-full"
              onClick={() => setVisibleCount((n) => n + PAGE_SIZE)}
            >
              {t("billing.history.loadMore")}
            </Button>
          )}
        </div>
      )}
    </SideDrawer>
  )
}
