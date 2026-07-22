import { useEffect } from "react"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconCircleX } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"

export function BillingPaymentCancelPage() {
  const { t } = useTranslation()
  const activeOrganizationId = localStorage.getItem("active_organization_id")

  useEffect(() => {
    const timer = setTimeout(() => window.close(), 5000)
    return () => clearTimeout(timer)
  }, [])

  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-4 text-center">
      <IconCircleX className="size-16 text-destructive" />
      <h1 className="text-xl font-bold">{t("billing.payment.cancelTitle")}</h1>
      <p className="max-w-xs text-sm text-muted-foreground">
        {t("billing.payment.cancelDescription")}
      </p>
      {activeOrganizationId ? (
        <Button
          variant="outline"
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
