import { useTranslation } from "react-i18next"
import { IconAlertTriangle } from "@tabler/icons-react"
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert"
import { useInvoicePreview } from "@/features/billing/hooks"

// allowed < 0 is the plan/addon "unlimited" sentinel used throughout this
// module (see planLimits/downgradeTargetLimits on the backend) — current
// can never be "over" an unlimited allowance.
function isOverLimit(metric?: { current: number; allowed: number }): boolean {
  return !!metric && metric.allowed >= 0 && metric.current > metric.allowed
}

interface Props {
  organizationId: string
}

// Reads GET .../billing/preview's current-state overage (present once a
// scheduled downgrade/addon decrease would leave usage over its future
// limit) and shows it purely for visibility — no call to action, since the
// renewal worker resolves it automatically. Shared by the addon section and
// the plan-downgrade UI rather than duplicated in each.
export function OverageWarningCard({ organizationId }: Props) {
  const { t } = useTranslation()
  const { data } = useInvoicePreview(organizationId)
  const overage = data?.data?.overage
  if (!overage) return null

  const members = overage.members
  const overMembers = isOverLimit(members)
  if (!overMembers) return null

  return (
    <Alert>
      <IconAlertTriangle />
      <AlertTitle>
        {t("billing.scheduledAmendments.overageWarning.title")}
      </AlertTitle>
      <AlertDescription>
        {overMembers && members && (
          <p>
            {t("billing.scheduledAmendments.overageWarning.members", {
              current: members.current,
              allowed: members.allowed,
            })}
          </p>
        )}
        <p>{t("billing.scheduledAmendments.overageWarning.autoResolved")}</p>
      </AlertDescription>
    </Alert>
  )
}
