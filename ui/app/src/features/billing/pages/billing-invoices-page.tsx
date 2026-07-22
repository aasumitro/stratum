import { useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { InvoicesTable } from "@/features/billing/components/invoices-table"

export function BillingInvoicesPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
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
