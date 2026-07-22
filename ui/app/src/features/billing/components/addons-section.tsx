import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  useAddonsCatalog,
  useAttachedAddons,
  useAttachAddon,
  useDetachAddon,
  useBillingSubscription,
} from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { formatPrice } from "@/features/billing/utils"

interface Props {
  organizationId: string
}

export function AddonsSection({ organizationId }: Props) {
  const { t } = useTranslation()
  const { isOwner } = usePermissions()
  const [quantities, setQuantities] = useState<Record<string, string>>({})
  const { data: catalogData, isLoading: catalogLoading } = useAddonsCatalog()
  const { data: attachedData, isLoading: attachedLoading } =
    useAttachedAddons(organizationId)
  const { data: subData } = useBillingSubscription(organizationId)
  const { mutate: attach, isPending: attaching } =
    useAttachAddon(organizationId)
  const { mutate: detach, isPending: detaching } =
    useDetachAddon(organizationId)

  const catalog = (catalogData?.data ?? []).filter((a) => a.active)
  const attached = attachedData?.data ?? []
  const attachedById = new Map(attached.map((a) => [a.addon_id, a]))
  const currency = subData?.data?.currency ?? "USD"
  const cycle = subData?.data?.cycle ?? "monthly"

  if (catalogLoading || attachedLoading) {
    return (
      <Card>
        <CardHeader>
          <Skeleton className="h-5 w-24" />
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {[1, 2].map((i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </CardContent>
      </Card>
    )
  }

  if (!catalog.length) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("billing.addons.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        {!isOwner && (
          <p className="mb-3 text-xs text-muted-foreground">
            {t("billing.managedByNote")}
          </p>
        )}
        <ul className="flex flex-col gap-3">
          {catalog.map((addon) => {
            const attachedRow = attachedById.get(addon.id)
            const isAttached = !!attachedRow
            const prices = addon.prices[currency] ?? addon.prices["USD"]
            const amount = prices
              ? cycle === "monthly"
                ? prices.monthly
                : prices.yearly
              : 0
            const quantity = quantities[addon.id] ?? "1"
            return (
              <li
                key={addon.id}
                className="flex items-center justify-between gap-3 text-sm"
              >
                <div className="flex flex-col">
                  <span className="font-medium">
                    {addon.name}
                    {isAttached && (
                      <span className="ml-2 rounded-full bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-600">
                        {t("billing.addons.activeQty", {
                          qty: attachedRow.quantity,
                        })}
                      </span>
                    )}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {addon.description} ·{" "}
                    {formatPrice(amount, currency, cycle, t)}
                  </span>
                </div>
                {isOwner &&
                  (isAttached ? (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={detaching}
                      onClick={() => detach(addon.id)}
                    >
                      {detaching && (
                        <IconLoader2
                          data-icon="inline-start"
                          className="animate-spin"
                        />
                      )}
                      {detaching
                        ? t("billing.addons.detaching")
                        : t("billing.addons.detach")}
                    </Button>
                  ) : (
                    <div className="flex items-center gap-2">
                      <Select
                        value={quantity}
                        onValueChange={(v) =>
                          setQuantities((prev) => ({
                            ...prev,
                            [addon.id]: v ?? "1",
                          }))
                        }
                      >
                        <SelectTrigger size="sm" className="w-16">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {[1, 2, 3, 4, 5].map((q) => (
                            <SelectItem key={q} value={String(q)}>
                              {q}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Button
                        size="sm"
                        disabled={attaching}
                        onClick={() =>
                          attach({
                            addon_id: addon.id,
                            quantity: Number(quantity),
                          })
                        }
                      >
                        {attaching && (
                          <IconLoader2
                            data-icon="inline-start"
                            className="animate-spin"
                          />
                        )}
                        {attaching
                          ? t("billing.addons.attaching")
                          : t("billing.addons.attach")}
                      </Button>
                    </div>
                  ))}
              </li>
            )
          })}
        </ul>
      </CardContent>
    </Card>
  )
}
