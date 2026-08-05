import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconX } from "@tabler/icons-react"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { SidebarCardStack } from "@/components/layout/sidebar-card-stack"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { useWebhooks } from "@/features/organization/hooks/use-webhooks"
import {
  useBillingFeatures,
  useBillingSubscription,
} from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { cn } from "@/lib/ui"

function daysLeft(iso?: string): number | null {
  if (!iso) return null
  const ms = new Date(iso).getTime() - Date.now()
  return ms > 0 ? Math.ceil(ms / (1000 * 60 * 60 * 24)) : 0
}

/**
 * Trial badge + first-run checklist, relocated from the removed Overview
 * page into the sidebar's card-stack slot — same slot future
 * announcement/changelog cards will use, one at a time, priority order.
 */
export function SidebarSetupCards({
  organizationId,
}: {
  organizationId: string
}) {
  const { t } = useTranslation()
  const perms = usePermissions()
  const { data: subscriptionData } = useBillingSubscription(organizationId)
  const { data: featuresData } = useBillingFeatures(organizationId)
  const membersFeature = (featuresData?.data ?? []).find(
    (f) => f.feature_id === "members"
  )
  // -1 is the "unlimited" sentinel (Custom plan); `limit` already includes
  // purchased add-on seats, same rule as the Settings page Members gate.
  const isMemberSeatLocked =
    membersFeature?.limit != null &&
    membersFeature.limit !== -1 &&
    membersFeature.limit <= 1
  const hasWebhooksFeature = (featuresData?.data ?? []).some(
    (f) => f.feature_id === "webhooks"
  )
  const { data: membersData } = useOrganizationMembers(organizationId)
  const { data: webhooksData } = useWebhooks(
    organizationId,
    perms.canAccessWebhooks
  )
  const subscription = subscriptionData?.data
  const trialDays = daysLeft(subscription?.trial_end)
  const memberCount = membersData?.data?.length ?? 0
  const webhookCount = webhooksData?.data?.length ?? 0

  const [checklistDismissed, setChecklistDismissed] = useState(
    () => localStorage.getItem(`checklist_dismissed_${organizationId}`) === "1"
  )
  function dismissChecklist() {
    localStorage.setItem(`checklist_dismissed_${organizationId}`, "1")
    setChecklistDismissed(true)
  }

  const checklist = [
    { key: "create", done: true, labelKey: "dashboard.checklist.create" },
    { key: "profile", done: true, labelKey: "dashboard.checklist.profile" },
    ...(!isMemberSeatLocked
      ? [
          {
            key: "invite",
            done: memberCount > 1,
            labelKey: "dashboard.checklist.invite",
            href: `/organization/${organizationId}/settings#members`,
          },
        ]
      : []),
    ...(perms.canAccessWebhooks && hasWebhooksFeature
      ? [
          {
            key: "webhook",
            done: webhookCount > 0,
            labelKey: "dashboard.checklist.webhook",
            href: `/organization/${organizationId}/settings?panel=webhooks`,
          },
        ]
      : []),
  ]
  const checklistDone = checklist.filter((c) => c.done).length
  const showChecklist = !checklistDismissed && checklistDone < checklist.length

  const trialCard = subscription?.status === "trialing" &&
    trialDays !== null && (
      <Card className="flex flex-col gap-2.5 rounded-xl p-3">
        <div className="flex items-center justify-between">
          <span className="text-sm font-semibold">
            {t("dashboard.freeTrial")}
          </span>
          <span className="text-xs text-muted-foreground">
            {t("dashboard.daysLeft", { count: trialDays })}
          </span>
        </div>
        <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary">
          <div
            className="h-full bg-primary transition-all"
            style={{
              width: `${Math.max(0, Math.min(100, (trialDays / 7) * 100))}%`,
            }}
          />
        </div>
      </Card>
    )

  const checklistCard = showChecklist && (
    <Card className="rounded-xl">
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
            {t("dashboard.checklist.title", {
              done: checklistDone,
              total: checklist.length,
            })}
          </span>
          <button
            onClick={dismissChecklist}
            className="text-muted-foreground hover:text-foreground"
            aria-label={t("common.dismiss")}
          >
            <IconX className="size-3.5" />
          </button>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {checklist.map((item) => (
          <span
            key={item.key}
            className={cn(
              "flex items-center gap-2 text-sm",
              item.done
                ? "text-muted-foreground line-through"
                : "hover:underline"
            )}
          >
            <span
              className={cn(
                "flex size-4 shrink-0 items-center justify-center rounded border",
                item.done && "border-primary bg-primary text-primary-foreground"
              )}
            >
              {item.done && "✓"}
            </span>
            {item.href ? (
              <a href={item.href}>{t(item.labelKey)}</a>
            ) : (
              t(item.labelKey)
            )}
          </span>
        ))}
      </CardContent>
    </Card>
  )

  return <SidebarCardStack cards={[trialCard, checklistCard]} />
}
