import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { useRotateWebhookSecret } from "@/features/organization/hooks"

interface Props {
  organizationId: string
  webhookId: string | null
  onOpenChange: (open: boolean) => void
}

// Secret rotation with a 24h dual-validity grace window (the previous
// secret keeps verifying alongside the new one server-side). Same
// shown-once + "I've stored it" pattern as create.
export function RotateSecretDialog({
  organizationId,
  webhookId,
  onOpenChange,
}: Props) {
  const { t } = useTranslation()
  const [secret, setSecret] = useState<string | null>(null)
  const [stored, setStored] = useState(false)
  const { mutate: rotate, isPending } = useRotateWebhookSecret(organizationId)

  function handleClose() {
    setSecret(null)
    setStored(false)
    onOpenChange(false)
  }

  return (
    <Dialog open={!!webhookId} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{t("organization.webhooks.rotateTitle")}</DialogTitle>
          <DialogDescription>
            {secret
              ? t("organization.webhooks.secretWarning")
              : t("organization.webhooks.rotateDescription")}
          </DialogDescription>
        </DialogHeader>

        {secret ? (
          <div className="flex flex-col gap-3">
            <code className="rounded-lg border bg-muted p-3 font-mono text-xs break-all select-all">
              {secret}
            </code>
            <div className="flex items-start gap-2">
              <Checkbox
                id="rotate-secret-stored"
                checked={stored}
                onCheckedChange={(v) => setStored(v === true)}
              />
              <Label
                htmlFor="rotate-secret-stored"
                className="text-sm font-normal"
              >
                {t("organization.webhooks.secretStoredAck")}
              </Label>
            </div>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            {t("organization.webhooks.rotateGraceNote")}
          </p>
        )}

        <DialogFooter>
          {secret ? (
            <Button onClick={handleClose} disabled={!stored}>
              {t("common.done")}
            </Button>
          ) : (
            <>
              <Button variant="outline" onClick={handleClose}>
                {t("common.cancel")}
              </Button>
              <Button
                disabled={isPending}
                onClick={() =>
                  webhookId &&
                  rotate(webhookId, {
                    onSuccess: (res) => {
                      if (res.data) setSecret(res.data.secret)
                    },
                  })
                }
              >
                {isPending && (
                  <IconLoader2
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                )}
                {t("organization.webhooks.rotateConfirm")}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
