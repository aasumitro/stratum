import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useBillingHistory } from "@/features/billing/hooks"
import { capitalize, timeAgo } from "@/lib/format"
import { SubscriptionHistorySheet } from "./subscription-history-sheet"

interface Props {
  organizationId: string
  isOwner: boolean
}

export function SubscriptionHistorySection({ organizationId, isOwner }: Props) {
  const { t } = useTranslation()
  const [showSheet, setShowSheet] = useState(false)
  const { data, isLoading } = useBillingHistory(organizationId, isOwner)

  if (!isOwner) return null

  if (isLoading) {
    return (
      <div className="flex-1 space-y-2">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-4 w-full" />
      </div>
    )
  }

  const history = data?.data ?? []
  const latest = history[0]
  if (!latest) return null

  const name = latest.changed_by_name
  const timeAgoLabel = timeAgo(latest.changed_at)
  let summary: string
  switch (latest.action) {
    case "upgrade":
    case "downgrade":
      summary = t("billing.history.summaryChange", {
        name,
        fromPlan: latest.from_plan ? capitalize(latest.from_plan) : "—",
        toPlan: latest.to_plan ? capitalize(latest.to_plan) : "—",
        timeAgo: timeAgoLabel,
      })
      break
    case "trial":
    case "activate":
      summary = t("billing.history.summaryStart", {
        name,
        toPlan: latest.to_plan ? capitalize(latest.to_plan) : "—",
        timeAgo: timeAgoLabel,
      })
      break
    case "cancel":
      summary = t("billing.history.summaryCancel", { name, timeAgo: timeAgoLabel })
      break
    case "resume":
      summary = t("billing.history.summaryResume", { name, timeAgo: timeAgoLabel })
      break
    case "expire":
      summary = t("billing.history.summaryExpire", { name, timeAgo: timeAgoLabel })
      break
    case "extend":
      summary = t("billing.history.summaryExtend", { name, timeAgo: timeAgoLabel })
      break
  }

  return (
    <div className="flex-1 space-y-2">
      <p className="text-sm font-medium text-muted-foreground">
        {t("billing.history.title")}
      </p>
      <p className="text-sm text-muted-foreground">{summary}</p>
      <Button
        variant="link"
        className="h-auto w-fit p-0 text-sm"
        onClick={() => setShowSheet(true)}
      >
        {t("billing.history.viewFull")}
      </Button>
      <SubscriptionHistorySheet
        organizationId={organizationId}
        open={showSheet}
        onOpenChange={setShowSheet}
      />
    </div>
  )
}
