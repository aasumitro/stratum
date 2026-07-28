import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2, IconX } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { useRotateWebhookSecret } from "@/features/organization/hooks"

interface Props {
  organizationId: string
  webhookId: string
  onOpenChange: (open: boolean) => void
}

// Same shown-once + "I've stored it" pattern as WebhookFormPanel's create
// flow, and the same slide-over slot (webhooks-panel.tsx renders this or
// WebhookFormPanel, never both) — rotation is a 24h dual-validity grace
// window, the previous secret keeps verifying alongside the new one
// server-side.
export function RotateSecretPanel({
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
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-start justify-between border-b px-4 py-4">
        <div className="min-w-0 flex-1">
          <h3 className="font-heading text-lg font-medium">
            {t("organization.webhooks.rotateTitle")}
          </h3>
          <p className="mt-1 text-xs break-all text-muted-foreground">
            {webhookId}
          </p>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("common.close")}
          onClick={handleClose}
          disabled={!!secret && !stored}
        >
          <IconX className="size-4" />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-4">
        <div className="flex flex-col gap-6">
          <p className="text-sm text-muted-foreground">
            {secret
              ? t("organization.webhooks.secretWarning")
              : t("organization.webhooks.rotateDescription")}
          </p>

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

          <div className="flex gap-2">
            {secret ? (
              <Button onClick={handleClose} disabled={!stored}>
                {t("common.done")}
              </Button>
            ) : (
              <>
                <Button
                  disabled={isPending}
                  onClick={() =>
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
                <Button type="button" variant="outline" onClick={handleClose}>
                  {t("common.cancel")}
                </Button>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
