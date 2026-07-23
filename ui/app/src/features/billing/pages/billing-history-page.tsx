import { useEffect } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconRobot, IconWebhook } from "@tabler/icons-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Skeleton } from "@/components/ui/skeleton"
import { useBillingHistory } from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { formatMoney, initials } from "@/lib/format"

function formatDate(s: string) {
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

// 2d — changed_by resolves to an avatar+name, or a system/webhook chip.
function ChangedByCell({
  name,
  kind,
}: {
  name: string
  kind: "user" | "system" | "webhook"
}) {
  if (kind === "system") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
        <IconRobot className="size-3" />
        {name}
      </span>
    )
  }
  if (kind === "webhook") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
        <IconWebhook className="size-3" />
        {name}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <span className="flex size-5 items-center justify-center rounded-full bg-primary/10 text-[10px] font-semibold text-primary">
        {initials(name)}
      </span>
      {name}
    </span>
  )
}

export function BillingHistoryPage() {
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

  const { data, isLoading } = useBillingHistory(organizationId)
  const history = data?.data ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("billing.history.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="flex flex-col gap-2">
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : !history.length ? (
          <p className="text-sm text-muted-foreground">
            {t("billing.history.noHistory")}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("billing.history.actionCol")}</TableHead>
                <TableHead>{t("billing.history.changeCol")}</TableHead>
                <TableHead>{t("billing.history.amountCol")}</TableHead>
                <TableHead>{t("billing.history.byCol")}</TableHead>
                <TableHead>{t("billing.history.dateCol")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.map((h) => (
                <TableRow key={h.id}>
                  <TableCell>
                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium capitalize">
                      {h.action}
                    </span>
                  </TableCell>
                  <TableCell className="text-sm">
                    <span className="text-muted-foreground capitalize">
                      {h.from_plan ?? "—"}
                    </span>{" "}
                    → <span className="capitalize">{h.to_plan ?? "—"}</span>
                  </TableCell>
                  <TableCell className="text-sm">
                    {h.amount_cents > 0
                      ? formatMoney(h.amount_cents, h.currency)
                      : "—"}
                  </TableCell>
                  <TableCell>
                    <ChangedByCell
                      name={h.changed_by_name}
                      kind={h.changed_by_kind}
                    />
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {formatDate(h.changed_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
