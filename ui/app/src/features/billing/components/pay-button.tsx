import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useHTTPActionPost } from "@/lib/api/action"
import { API } from "@/lib/api/path"
import { capture } from "@/lib/analytics"
import { formatMoney } from "@/lib/format"
import { useEligibleCoupons, useRedeemCoupon } from "@/features/billing/hooks"
import type { PaymentLink } from "@/types/billing"

interface Props {
  invoiceId: string
  organizationId: string
  amountCents: number
  currency: string
  // An already-open pending link relabels the button; POST .../pay is
  // idempotent server-side (reuses the existing link), so the click handler
  // doesn't need to change, just the copy.
  awaitingPayment?: boolean
}

// The Pay dialog lists eligible coupons for explicit Apply (never
// auto-applied) before continuing to the hosted checkout page. Applying a
// coupon here calls the same subscription-level redeem endpoint the
// standalone redeem flow used — it takes effect on the *next* invoice, not
// retroactively on the one currently being paid (this backend has no
// invoice-level coupon application), so the dialog deliberately doesn't
// claim the total shown updates immediately — that's a known limitation,
// not a bug.
export function PayButton({
  invoiceId,
  organizationId,
  amountCents,
  currency,
  awaitingPayment,
}: Props) {
  const { t } = useTranslation()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [opening, setOpening] = useState(false)
  const [code, setCode] = useState("")
  const winRef = useRef<Window | null>(null)

  const { data: couponsData } = useEligibleCoupons(organizationId, dialogOpen)
  const coupons = couponsData?.data ?? []
  const { mutate: redeem, isPending: redeeming } =
    useRedeemCoupon(organizationId)

  const { mutate } = useHTTPActionPost<PaymentLink>({
    url: API.billing(organizationId, "invoices", invoiceId, "pay"),
    options: {
      onMutate: () => {
        setOpening(true)
        // Open window synchronously in click handler to bypass popup blockers
        winRef.current = window.open("", "_blank")
      },
      onSuccess: (res) => {
        const url = res.data?.url
        capture("invoice_paid", {
          invoice_id: invoiceId,
          organization_id: organizationId,
        })
        if (url && winRef.current && !winRef.current.closed) {
          winRef.current.location.href = url
        } else {
          winRef.current?.close()
          if (url) window.location.href = url
        }
        winRef.current = null
        setOpening(false)
        setDialogOpen(false)
      },
      onError: () => {
        winRef.current?.close()
        winRef.current = null
        toast.error(t("billing.invoices.payFailed"))
        setOpening(false)
      },
    },
  })

  return (
    <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
      <DialogTrigger render={<Button size="sm" variant="outline" />}>
        {awaitingPayment
          ? t("billing.invoices.awaitingPayment")
          : t("billing.invoices.pay")}
      </DialogTrigger>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{t("billing.pay.title")}</DialogTitle>
          <DialogDescription>{t("billing.pay.description")}</DialogDescription>
        </DialogHeader>

        <div className="rounded-lg bg-muted p-3">
          <div className="flex items-center justify-between text-sm font-semibold">
            <span>{t("billing.pay.total")}</span>
            <span>{formatMoney(amountCents, currency)}</span>
          </div>
        </div>

        <div className="flex flex-col gap-2">
          <p className="text-xs font-semibold text-muted-foreground">
            {t("billing.pay.availableCoupons")}
          </p>
          {coupons.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              {t("billing.pay.noCoupons")}
            </p>
          ) : (
            coupons.map((c) => (
              <div
                key={c.code}
                className="flex items-center justify-between gap-2 text-sm"
              >
                <div>
                  <span className="font-medium">{c.code}</span>{" "}
                  <span className="text-xs text-muted-foreground">
                    {c.discount_type === "percent"
                      ? `−${c.percent_off}%`
                      : `−${formatMoney(c.amount_cents ?? 0, currency)}`}{" "}
                    · {t(`billing.coupons.cadence.${c.cadence}`)}
                  </span>
                </div>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={redeeming}
                  onClick={() => redeem({ code: c.code })}
                >
                  {t("billing.coupons.apply")}
                </Button>
              </div>
            ))
          )}
          <div className="flex gap-2 pt-1">
            <Input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder={t("billing.coupons.codePlaceholder")}
              disabled={redeeming}
              className="h-8 text-sm"
            />
            <Button
              size="sm"
              variant="outline"
              disabled={redeeming || !code.trim()}
              onClick={() =>
                redeem({ code: code.trim() }, { onSuccess: () => setCode("") })
              }
            >
              {t("billing.coupons.apply")}
            </Button>
          </div>
          <p className="text-[11px] text-muted-foreground">
            {t("billing.pay.couponTimingNote")}
          </p>
        </div>

        <DialogFooter>
          <Button
            disabled={opening}
            onClick={() => mutate()}
            className="w-full"
          >
            {opening && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {opening
              ? t("billing.invoices.opening")
              : t("billing.pay.continue")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
