import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconArrowRight } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useBillingFeatures,
  usePlans,
  useFeatures,
  useBillingSubscription,
} from "@/features/billing/hooks"
import { PlanFeaturesSheet, type TierGroup } from "./plan-features-sheet"

interface Props {
  organizationId: string
}

export function FeaturesSection({ organizationId }: Props) {
  const { t } = useTranslation()
  const [showSheet, setShowSheet] = useState(false)
  const { data: featData, isLoading: featLoading } =
    useBillingFeatures(organizationId)
  const { data: subData } = useBillingSubscription(organizationId)
  const { data: plansData } = usePlans()
  const { data: catalogData } = useFeatures()

  const entitlements = featData?.data ?? []
  const grantedIds = new Set(entitlements.map((e) => e.feature_id))
  const currentPlan = plansData?.data?.find((p) => p.id === subData?.data?.plan)
  const featureNames = new Map(
    (catalogData?.data ?? []).map((f) => [f.id, f.name])
  )

  // Attribute each locked feature to the lowest-tier plan above the current
  // one that grants it — the closest upgrade a user could make to get it —
  // instead of flattening every higher tier's features into one list.
  const tierGroups: TierGroup[] = []
  if (plansData?.data && currentPlan) {
    const higherTiers = [...plansData.data]
      .filter((p) => p.sort_order > currentPlan.sort_order)
      .sort((a, b) => a.sort_order - b.sort_order)

    const attributed = new Set<string>()
    for (const plan of higherTiers) {
      // A plan's own feature set is boolean/static ids (plan.features) plus
      // config ids (keys of plan.config_values).
      const planFeatureIds = [
        ...(plan.features ?? []),
        ...Object.keys(plan.config_values ?? {}),
      ]
      const featureIds = planFeatureIds.filter(
        (f) => !grantedIds.has(f) && !attributed.has(f)
      )
      featureIds.forEach((f) => attributed.add(f))
      if (featureIds.length) tierGroups.push({ plan, featureIds })
    }
  }

  if (featLoading) {
    return (
      <div className="space-y-1">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-6 w-20" />
        <Skeleton className="mt-2 h-4 w-24" />
      </div>
    )
  }

  // Metered entitlements render in UsageMeters (with the plan/addon split)
  // — showing them here too would duplicate the same bars.
  const presence = entitlements.filter(
    (e) => e.type === "boolean" || e.type === "static"
  )
  const config = entitlements.filter((e) => e.type === "config")
  const includedCount = presence.length + config.length
  const lockedCount = tierGroups.reduce((n, g) => n + g.featureIds.length, 0)

  if (!includedCount && !lockedCount) return null

  return (
    <div className="space-y-1">
      <p className="text-sm font-medium text-muted-foreground">
        {t("billing.features.title")}
      </p>
      <p className="text-lg font-semibold">
        {t("billing.features.includedCount", { count: includedCount })}
      </p>
      <Button
        variant="link"
        className="mt-2 h-auto w-fit gap-1 p-0 text-xs"
        onClick={() => setShowSheet(true)}
      >
        {t("billing.features.viewAll")}
        <IconArrowRight className="size-3" />
      </Button>
      <PlanFeaturesSheet
        organizationId={organizationId}
        open={showSheet}
        onOpenChange={setShowSheet}
        presence={presence}
        config={config}
        tierGroups={tierGroups}
        featureNames={featureNames}
      />
    </div>
  )
}
