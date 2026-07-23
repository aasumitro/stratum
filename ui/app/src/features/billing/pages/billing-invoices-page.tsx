import { useEffect } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { InvoicesTable } from "@/features/billing/components/invoices-table"
import { usePermissions } from "@/hooks/use-permissions"

export function BillingInvoicesPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const navigate = useNavigate()
  const { isOwner } = usePermissions()

  useEffect(() => {
    if (!isOwner) {
      toast(t("billing.invoicesOwnerOnlyRedirect"))
      void navigate({
        to: "/organization/$organizationId/billing",
        params: { organizationId },
      })
    }
  }, [isOwner, navigate, organizationId, t])
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("billing.invoices.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        <InvoicesTable organizationId={organizationId} />
      </CardContent>
    </Card>
  )
}
