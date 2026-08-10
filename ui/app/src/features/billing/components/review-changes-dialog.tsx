import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { useAttachAddon, useDetachAddon } from "@/features/billing/hooks"
import {
  computeAmendmentDiff,
  applyAmendmentDiff,
  type AmendmentChange,
} from "@/features/billing/utils"
import type { AttachedAddon } from "@/types/billing"
import type { Addon } from "@/types/reference"

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  staged: Record<string, number>
  currentlyAttached: AttachedAddon[]
  catalog: Addon[]
  periodEnd?: string
  organizationId: string
}

// Confirms the picker's staged addon selection, splitting it into an
// increase (computeAmendmentDiff's "immediate" bucket) vs. what the backend
// will actually apply at renewal (a decrease or removal) — re-derived fresh
// from `staged`/`currentlyAttached` on every render, so a Cancel here never
// leaves stale state for the next time this dialog opens. Fires the same
// attach/detach mutations either bucket resolves to; it's the backend's own
// status branch, not this dialog, that decides what a given call actually
// does. This dialog only ever renders for a non-trialing subscription
// (AddonsSection routes trialing changes through its own immediate-apply
// confirmation instead) — so "immediate" here no longer means the limit
// rises on confirm: an increase now creates a day-prorated invoice and only
// raises the limit once that invoice is paid. The bucket name (and
// computeAmendmentDiff itself) stays as-is; only this section's copy
// changed to reflect that.
export function ReviewChangesDialog({
  open,
  onOpenChange,
  staged,
  currentlyAttached,
  catalog,
  periodEnd,
  organizationId,
}: Props) {
  const { t } = useTranslation()
  const { mutateAsync: attach } = useAttachAddon(organizationId)
  const { mutateAsync: detach } = useDetachAddon(organizationId)
  const [submitting, setSubmitting] = useState(false)
  const [failed, setFailed] = useState<AmendmentChange[]>([])

  const diff = computeAmendmentDiff(staged, currentlyAttached)
  const nameById = new Map<string, string>()
  catalog.forEach((a) => nameById.set(a.id, a.name))
  currentlyAttached.forEach((a) => nameById.set(a.addon_id, a.name))

  async function handleConfirm() {
    setSubmitting(true)
    const stillFailed = await applyAmendmentDiff(diff, attach, detach)
    setSubmitting(false)
    setFailed(stillFailed)
    if (stillFailed.length === 0) onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t("billing.scheduledAmendments.reviewChanges.title")}
          </DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2 text-sm">
          {diff.immediate.length > 0 && (
            <div className="flex flex-col gap-2">
              <p className="font-medium">
                {t("billing.scheduledAmendments.reviewChanges.pendingPayment")}
              </p>
              <ul className="flex flex-col gap-1 text-muted-foreground">
                {diff.immediate.map((c) => (
                  <li key={c.addonId}>
                    {t("billing.scheduledAmendments.reviewChanges.changeLine", {
                      name: nameById.get(c.addonId) ?? c.addonId,
                      from: c.fromQty,
                      to: c.toQty,
                    })}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {diff.scheduled.length > 0 && (
            <div className="flex flex-col gap-2">
              <p className="font-medium">
                {t(
                  "billing.scheduledAmendments.reviewChanges.effectiveAtRenewal",
                  { date: formatDate(periodEnd) }
                )}
              </p>
              <ul className="flex flex-col gap-1 text-muted-foreground">
                {diff.scheduled.map((c) => (
                  <li key={c.addonId}>
                    {t("billing.scheduledAmendments.reviewChanges.changeLine", {
                      name: nameById.get(c.addonId) ?? c.addonId,
                      from: c.fromQty,
                      to: c.toQty,
                    })}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {failed.length > 0 && (
            <p className="rounded-md border border-destructive/20 bg-destructive/10 p-3 text-destructive">
              {t("billing.scheduledAmendments.reviewChanges.partialFailure")}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
          >
            {t("common.cancel")}
          </Button>
          <Button disabled={submitting} onClick={handleConfirm}>
            {submitting && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {t("billing.scheduledAmendments.reviewChanges.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
