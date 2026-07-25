import { useTranslation } from "react-i18next"
import { Link } from "@tanstack/react-router"
import { IconCheck, IconLock } from "@tabler/icons-react"
import { SideDrawer } from "@/components/shared/side-drawer"
import type { Entitlement } from "@/types/billing"
import type { Plan } from "@/types/reference"

export interface TierGroup {
  plan: Plan
  featureIds: string[]
}

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (open: boolean) => void
  presence: Entitlement[]
  config: Entitlement[]
  tierGroups: TierGroup[]
  featureNames: Map<string, string>
}

export function PlanFeaturesSheet({
  organizationId,
  open,
  onOpenChange,
  presence,
  config,
  tierGroups,
  featureNames,
}: Props) {
  const { t } = useTranslation()
  const billingPath = `/organization/${organizationId}/billing`

  return (
    <SideDrawer
      open={open}
      onOpenChange={onOpenChange}
      title={t("billing.features.title")}
      description={t("billing.features.description")}
      className="sm:max-w-lg"
    >
      <div className="flex flex-col gap-5 py-4">
        <div className="flex flex-col gap-3">
          <span className="text-sm font-medium">
            {t("billing.features.includedTitle")}
          </span>
          <ul className="flex flex-col gap-3">
            {presence.map((e) => (
              <li
                key={e.feature_id}
                className="flex items-center gap-2 text-sm"
              >
                <IconCheck className="size-4 shrink-0 text-emerald-500" />
                <span>{e.name}</span>
              </li>
            ))}
            {config.map((e) => (
              <li
                key={e.feature_id}
                className="flex items-center gap-2 text-sm"
              >
                <IconCheck className="size-4 shrink-0 text-emerald-500" />
                <span>{e.name}</span>
                <span className="text-xs text-muted-foreground">
                  {typeof e.config_value === "object"
                    ? JSON.stringify(e.config_value)
                    : String(e.config_value)}
                </span>
              </li>
            ))}
          </ul>
        </div>

        {tierGroups.map(({ plan, featureIds }) => (
          <div key={plan.id} className="flex flex-col gap-3 border-t pt-4">
            <span className="text-sm font-medium">
              {t("billing.features.unlockWith", { plan: plan.name })}
            </span>
            <ul className="flex flex-col gap-3">
              {featureIds.map((f) => (
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
          </div>
        ))}
      </div>
    </SideDrawer>
  )
}
