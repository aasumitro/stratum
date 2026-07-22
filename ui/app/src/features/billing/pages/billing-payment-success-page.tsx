import { useEffect } from "react"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconCircleCheck } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"

export function BillingPaymentSuccessPage() {
  const { t } = useTranslation()
  const activeOrganizationId = localStorage.getItem("active_organization_id")

  useEffect(() => {
    const timer = setTimeout(() => window.close(), 5000)
    return () => clearTimeout(timer)
  }, [])

  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-4 text-center">
      <IconCircleCheck className="size-16 text-emerald-500" />
      <h1 className="text-xl font-bold">{t("billing.payment.successTitle")}</h1>
      <p className="max-w-xs text-sm text-muted-foreground">
        {t("billing.payment.successDescription")}
      </p>
      {activeOrganizationId ? (
        <Button
          render={
            <Link
              to="/organization/$organizationId/billing"
              params={{ organizationId: activeOrganizationId }}
            />
          }
        >
          {t("billing.payment.viewBilling")}
        </Button>
      ) : (
        <Button variant="outline" render={<Link to="/organizations" />}>
          {t("billing.payment.goToOrganizations")}
        </Button>
      )}
    </div>
  )
}
