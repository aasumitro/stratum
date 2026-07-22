import { useTranslation } from "react-i18next"
import { IconCheck, IconLock } from "@tabler/icons-react"
import { Link } from "@tanstack/react-router"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useBillingFeatures,
  usePlans,
  useFeatures,
  useBillingSubscription,
} from "@/features/billing/hooks"

interface Props {
  organizationId: string
}

export function FeaturesSection({ organizationId }: Props) {
  const { t } = useTranslation()
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

  // collect all features from higher-tier plans (by sort_order) to show as locked
  const lockedFeatureIds: string[] = []
  if (plansData?.data && currentPlan) {
    for (const plan of plansData.data) {
      if (plan.sort_order <= currentPlan.sort_order) continue
      for (const f of plan.features ?? []) {
        if (!grantedIds.has(f) && !lockedFeatureIds.includes(f)) {
          lockedFeatureIds.push(f)
        }
      }
    }
  }

  if (featLoading) {
    return (
      <Card>
        <CardHeader>
          <Skeleton className="h-5 w-32" />
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-4 w-48" />
          ))}
        </CardContent>
      </Card>
    )
  }

  const billingPath = `/organization/${organizationId}/billing`
  // Metered entitlements render in UsageMeters (with the plan/addon split)
  // — showing them here too would duplicate the same bars.
  const presence = entitlements.filter(
    (e) => e.type === "boolean" || e.type === "static"
  )
  const config = entitlements.filter((e) => e.type === "config")

  if (!presence.length && !config.length && !lockedFeatureIds.length)
    return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("billing.features.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="flex flex-col gap-3">
          {presence.map((e) => (
            <li key={e.feature_id} className="flex items-center gap-2 text-sm">
              <IconCheck className="size-4 shrink-0 text-emerald-500" />
              <span>{e.name}</span>
            </li>
          ))}
          {config.map((e) => (
            <li key={e.feature_id} className="flex items-center gap-2 text-sm">
              <IconCheck className="size-4 shrink-0 text-emerald-500" />
              <span>{e.name}</span>
              <span className="text-xs text-muted-foreground">
                {typeof e.config_value === "object"
                  ? JSON.stringify(e.config_value)
                  : String(e.config_value)}
              </span>
            </li>
          ))}
          {lockedFeatureIds.map((f) => (
            <li
              key={f}
              className="flex items-center gap-2 text-sm text-muted-foreground"
            >
              <IconLock className="size-4 shrink-0" />
              <span>{featureNames.get(f) ?? f}</span>
              <Link
                to={billingPath}
                className="ml-auto text-xs text-primary hover:underline"
              >
                {t("billing.features.upgrade")}
              </Link>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
