import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  useActivateTrialNow,
  useInvoicePreview,
} from "@/features/billing/hooks"
import { InvoicePreviewNote } from "./invoice-preview-note"
import { formatMoney } from "@/lib/format"
import type { Invoice } from "@/types/billing"

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

interface Props {
  organizationId: string
}

type Step = "review" | "success"

// Skipping the rest of a trial creates a real invoice for the current
// plan's full period — it's charged on payment, not on click (see
// activateTrialNow in service_subscription_billing.go) — so this needs the
// same review-the-actual-amount + success pattern as Extend/Upgrade/
// Downgrade, not a bare confirm dialog with no number in it.
export function ActivateTrialDialog({ organizationId }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState<Step>("review")
  const [result, setResult] = useState<Invoice | null>(null)

  const { mutate: activateNow, isPending: activating } =
    useActivateTrialNow(organizationId)
  const { data: previewData, isFetching: previewLoading } = useInvoicePreview(
    organizationId,
    undefined,
    undefined,
    open && step === "review"
  )
  const preview = previewData?.data

  function handleClose(v: boolean) {
    if (!v) {
      setOpen(false)
      setTimeout(() => {
        setStep("review")
        setResult(null)
      }, 300)
      return
    }
    setOpen(v)
  }

  function handleConfirm() {
    activateNow(undefined, {
      onSuccess: (data) => {
        setResult(data.data ?? null)
        setStep("success")
      },
    })
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        {t("billing.trial.activateNow")}
      </Button>

      {step === "success" && (
        <Dialog open={open} onOpenChange={handleClose}>
          <DialogContent className="sm:max-w-sm">
            <DialogHeader>
              <DialogTitle>
                {t("billing.trial.activateSuccessTitle")}
              </DialogTitle>
              <DialogDescription>
                {t("billing.trial.activateSuccessDescription")}
              </DialogDescription>
            </DialogHeader>

            {result && (
              <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">
                    {t("billing.extend.amountDueLabel")}
                  </span>
                  <span className="font-medium">
                    {formatMoney(result.amount_cents, result.currency)}
                  </span>
                </div>
                {result.due_at && (
                  <div className="flex items-center justify-between">
                    <span className="text-muted-foreground">
                      {t("billing.extend.dueDateLabel")}
                    </span>
                    <span className="font-medium">
                      {formatDate(result.due_at)}
                    </span>
                  </div>
                )}
                <p className="pt-1 text-xs text-muted-foreground">
                  {t("billing.trial.activatePendingPaymentNote")}
                </p>
              </div>
            )}

            <DialogFooter>
              <Button onClick={() => handleClose(false)}>
                {t("common.done")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {step === "review" && (
        <Dialog open={open} onOpenChange={handleClose}>
          <DialogContent className="sm:max-w-sm">
            <DialogHeader>
              <DialogTitle>{t("billing.trial.activateNowTitle")}</DialogTitle>
              <DialogDescription>
                {t("billing.trial.activateNowDescription")}
              </DialogDescription>
            </DialogHeader>

            <InvoicePreviewNote preview={preview} loading={previewLoading} />

            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => handleClose(false)}
                disabled={activating}
              >
                {t("common.cancel")}
              </Button>
              <Button
                onClick={handleConfirm}
                disabled={activating || previewLoading}
              >
                {activating && (
                  <IconLoader2
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                )}
                {activating
                  ? t("billing.trial.activating")
                  : t("billing.trial.activateNow")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  )
}
