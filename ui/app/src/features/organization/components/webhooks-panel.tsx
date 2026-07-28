import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  IconPlus,
  IconWebhook,
  IconAlertTriangle,
  IconEdit,
  IconKey,
  IconSend,
  IconTrash,
  IconLoader2,
} from "@tabler/icons-react"
import { cn } from "@/lib/ui"
import { Card } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import { PermissionGuard } from "@/components/shared/permission-guard"
import { OverlayPanel } from "@/components/shared/overlay-panel"
import { FeatureGateCard } from "@/components/shared/feature-gate-card"
import {
  useWebhooksContentState,
  useDeleteWebhook,
  useSendWebhookTestEvent,
} from "@/features/organization/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { WebhookFormPanel } from "@/features/organization/components/webhook-form-panel"
import { RotateSecretPanel } from "@/features/organization/components/rotate-secret-panel"
import { WebhookDeliveriesPanel } from "@/features/organization/components/webhook-deliveries-panel"
import { WebhookSparkline } from "@/features/organization/components/webhook-sparkline"
import { healthTone } from "@/features/organization/utils/webhook-health"
import type { WebhookEndpoint } from "@/types/organization"

interface WebhooksPanelProps {
  organizationId: string
  open: boolean
  webhookId?: string
  onOpenChange: (open: boolean) => void
  onWebhookIdChange?: (id: string | undefined) => void
}

/**
 * Full Webhooks feature body — list, add/edit/delete, rotate secret, test
 * event, inline delivery history. Shared by the standalone Webhooks page and
 * the Settings page's "View All Webhooks" overlay, so this is the one place
 * webhooks business logic lives.
 */
export function WebhooksPanel({
  organizationId,
  open,
  webhookId,
  onOpenChange,
  onWebhookIdChange,
}: WebhooksPanelProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { canAccessWebhooks } = usePermissions()

  const { state, hasWebhooksFeature, endpoints } = useWebhooksContentState(
    organizationId,
    canAccessWebhooks
  )
  const { mutate: deleteWebhook, isPending: deleting } =
    useDeleteWebhook(organizationId)
  const { mutate: sendTestEvent, isPending: sendingTest } =
    useSendWebhookTestEvent(organizationId)
  const [addOpen, setAddOpen] = useState(false)
  const [editing, setEditing] = useState<WebhookEndpoint | undefined>()
  const [rotating, setRotating] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<WebhookEndpoint | null>(null)

  const viewDetail = endpoints.find((e) => e.id === webhookId) || null

  // Base UI's outside-press dismiss for a nested Popover doesn't stop the
  // triggering click from also reaching whatever is underneath it
  // (confirmed in @base-ui/react's useDismiss source — no stopPropagation
  // before it closes). Delete is a real nested modal opened on top of this
  // already-open panel, so a stray outside click could both dismiss it
  // *and* fire a button behind it (e.g. Back, Edit). Disabling pointer
  // events on this panel's own content while it's open is a hard CSS
  // guarantee against that, independent of the library's internal dismiss
  // timing. Rotate Secret doesn't need this — it's the same slide-over
  // pattern as the add/edit form, blocked by its own backdrop button
  // instead of a Base UI dismiss handler.
  const nestedModalOpen = !!deleteTarget

  function handleTestEvent(id: string) {
    sendTestEvent(id, {
      onSuccess: (res) => {
        if (res.data?.success) {
          toast.success(t("organization.webhooks.testEventSuccess"))
        } else {
          toast.error(t("organization.webhooks.testEventFailed"))
        }
      },
    })
  }

  return (
    <OverlayPanel
      open={open}
      onOpenChange={onOpenChange}
      title={t("organization.webhooks.title")}
      description={t("organization.webhooks.description")}
      bodyClassName="p-0"
    >
      <PermissionGuard
        allowed={canAccessWebhooks}
        explainer={{
          title: t("organization.webhooks.deniedTitle"),
          description: t("organization.webhooks.deniedDescription"),
        }}
      >
        <div className="flex h-full min-h-0 flex-1 overflow-hidden">
          <div
            className={cn(
              "flex h-full w-full flex-col overflow-y-auto",
              nestedModalOpen && "pointer-events-none"
            )}
          >
            {state === "loading" ? (
              <div className="flex flex-col gap-3">
                {[1, 2].map((i) => (
                  <Skeleton key={i} className="h-24 w-full rounded-lg" />
                ))}
              </div>
            ) : state === "locked" ? (
              <div className="flex h-full items-center justify-center pb-12">
                <FeatureGateCard
                  reason="plan"
                  title={t("organization.webhooks.upgradeTitle")}
                  description={t("organization.webhooks.upgradeDescription")}
                  cta={{
                    label: t("organization.webhooks.upgradeCta"),
                    onClick: () =>
                      void navigate({
                        to: "/organization/$organizationId/billing",
                        params: { organizationId },
                      }),
                  }}
                />
              </div>
            ) : viewDetail ? (
              <div className="flex h-full flex-col">
                <div className="flex shrink-0 items-center justify-between border-b px-4 py-4 md:px-6 md:py-6">
                  <div className="flex min-w-0 flex-1 items-center gap-3">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="-ml-2 text-muted-foreground hover:text-foreground"
                      onClick={() => onWebhookIdChange?.(undefined)}
                    >
                      &larr; {t("common.back")}
                    </Button>
                    <div className="min-w-0 flex-1">
                      <h3 className="truncate font-heading text-lg font-medium">
                        {viewDetail.url}
                      </h3>
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      className="h-8 gap-1.5"
                      onClick={() => setEditing(viewDetail)}
                    >
                      <IconEdit className="size-3.5" />
                      <span className="hidden sm:inline">
                        {t("organization.webhooks.edit")}
                      </span>
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      className="h-8 gap-1.5"
                      onClick={() => setRotating(viewDetail.id)}
                    >
                      <IconKey className="size-3.5" />
                      <span className="hidden sm:inline">
                        {t("organization.webhooks.rotateSecret")}
                      </span>
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      className="h-8 gap-1.5"
                      onClick={() => handleTestEvent(viewDetail.id)}
                      disabled={sendingTest}
                    >
                      <IconSend className="size-3.5" />
                      <span className="hidden sm:inline">
                        {t("organization.webhooks.sendTestEvent")}
                      </span>
                    </Button>
                    <Popover
                      open={!!deleteTarget}
                      onOpenChange={(open) => !open && setDeleteTarget(null)}
                    >
                      <PopoverTrigger
                        render={
                          <Button
                            variant="destructive"
                            size="sm"
                            className="h-8 gap-1.5"
                            onClick={() => setDeleteTarget(viewDetail)}
                          />
                        }
                      >
                        <IconTrash className="size-3.5" />
                        <span className="hidden sm:inline">
                          {t("organization.webhooks.delete")}
                        </span>
                      </PopoverTrigger>
                      <PopoverContent align="end">
                        <PopoverHeader>
                          <PopoverTitle>
                            {t("organization.webhooks.deleteTitle")}
                          </PopoverTitle>
                          <PopoverDescription>
                            {t("organization.webhooks.deleteDescription")}
                          </PopoverDescription>
                        </PopoverHeader>
                        <div className="flex justify-end gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => setDeleteTarget(null)}
                          >
                            {t("common.cancel")}
                          </Button>
                          <Button
                            variant="destructive"
                            size="sm"
                            disabled={deleting}
                            onClick={() =>
                              deleteTarget &&
                              deleteWebhook(deleteTarget.id, {
                                onSuccess: () => {
                                  setDeleteTarget(null)
                                  if (viewDetail?.id === deleteTarget.id) {
                                    onWebhookIdChange?.(undefined)
                                  }
                                },
                              })
                            }
                          >
                            {deleting && (
                              <IconLoader2
                                data-icon="inline-start"
                                className="animate-spin"
                              />
                            )}
                            {t("common.delete")}
                          </Button>
                        </div>
                      </PopoverContent>
                    </Popover>
                  </div>
                </div>
                <div className="flex-1 overflow-y-auto px-4 py-4 md:p-6">
                  <WebhookDeliveriesPanel
                    organizationId={organizationId}
                    endpoint={viewDetail}
                  />
                </div>
              </div>
            ) : (
              <div className="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2 md:grid-cols-3 md:p-6 2xl:grid-cols-4">
                {hasWebhooksFeature && (
                  <button
                    type="button"
                    onClick={() => setAddOpen(true)}
                    className="flex min-h-[100px] flex-col items-center justify-center rounded-lg border-2 border-dashed bg-transparent p-4 text-muted-foreground transition-colors hover:border-primary hover:text-primary"
                  >
                    <IconPlus className="mb-1.5 size-6" />
                    <span className="text-xs font-medium">
                      {t("organization.webhooks.add")}
                    </span>
                  </button>
                )}
                {[...endpoints]
                  .sort((a, b) => {
                    if (a.enabled !== b.enabled) return a.enabled ? -1 : 1
                    if (!!a.auto_disabled_at !== !!b.auto_disabled_at)
                      return a.auto_disabled_at ? 1 : -1
                    const aPercent = a.health?.success_percent_24h ?? 100
                    const bPercent = b.health?.success_percent_24h ?? 100
                    if (aPercent !== bPercent) return bPercent - aPercent
                    return a.url.localeCompare(b.url)
                  })
                  .map((ep) => {
                    const percent = ep.health?.success_percent_24h ?? 100
                    return (
                      <Card
                        key={ep.id}
                        className="flex cursor-pointer flex-col transition-colors hover:border-primary"
                        onClick={() => onWebhookIdChange?.(ep.id)}
                      >
                        <div className="flex h-full flex-col p-3">
                          <div className="mb-4 flex items-start justify-between gap-2">
                            <div className="flex min-w-0 items-start gap-2.5">
                              <div
                                className={cn(
                                  "flex h-7 w-7 shrink-0 items-center justify-center rounded-md border shadow-sm",
                                  ep.enabled
                                    ? "border-primary/20 bg-primary/5 text-primary"
                                    : "border-muted bg-muted/50 text-muted-foreground"
                                )}
                              >
                                <IconWebhook className="size-3.5" />
                              </div>
                              <div className="flex min-w-0 flex-1 flex-col gap-0.5 pt-0.5">
                                <p className="truncate font-mono text-[11px] font-medium text-foreground/90">
                                  {ep.url}
                                </p>
                                <p className="truncate text-[10px] text-muted-foreground">
                                  {!ep.enabled
                                    ? t("organization.webhooks.disabledLabel")
                                    : !ep.subscribed_events?.length
                                      ? t(
                                          "organization.webhooks.allEventsLabel"
                                        )
                                      : t(
                                          "organization.webhooks.subscribedEventsLabel",
                                          {
                                            count: ep.subscribed_events.length,
                                          }
                                        )}
                                </p>
                              </div>
                            </div>
                          </div>

                          {ep.auto_disabled_at && (
                            <div className="mb-3 flex items-center gap-1.5 rounded bg-destructive/10 px-2 py-1 text-destructive">
                              <IconAlertTriangle className="size-3 shrink-0" />
                              <span className="text-[10px] font-medium">
                                {t("organization.webhooks.autoDisabled")}
                              </span>
                            </div>
                          )}

                          <div className="mt-auto flex flex-col gap-3">
                            <div className="flex items-baseline gap-1.5">
                              <span
                                className={cn(
                                  "text-2xl leading-none font-bold tracking-tight",
                                  healthTone(percent, true)
                                )}
                              >
                                {ep.health ? `${percent}%` : "—"}
                              </span>
                              <span className="text-[9px] font-medium tracking-wider text-muted-foreground/60 uppercase">
                                {t("organization.webhooks.successRate")}
                              </span>
                            </div>

                            <div className="border-t border-dashed pt-2">
                              {ep.health ? (
                                <WebhookSparkline
                                  organizationId={organizationId}
                                  webhookId={ep.id}
                                />
                              ) : (
                                <div className="flex h-6 items-center justify-center rounded border border-dashed border-muted bg-muted/10 text-[10px] tracking-wide text-muted-foreground/50 uppercase">
                                  {t("organization.webhooks.noSparklineData")}
                                </div>
                              )}
                            </div>
                          </div>
                        </div>
                      </Card>
                    )
                  })}
              </div>
            )}
          </div>

          {/* Slide-over panel for Form / Rotate Secret */}
          {(addOpen || editing || rotating) && (
            <button
              type="button"
              className="absolute inset-0 z-10 bg-black/20"
              onClick={() => {
                setAddOpen(false)
                setEditing(undefined)
                setRotating(null)
              }}
            />
          )}
          <div
            className={cn(
              "absolute inset-y-0 right-0 z-20 flex w-full flex-col border-l bg-popover transition-transform duration-200 sm:w-[32rem]",
              addOpen || editing || rotating
                ? "translate-x-0 shadow-xl"
                : "translate-x-full shadow-none"
            )}
          >
            {(addOpen || editing) && (
              <WebhookFormPanel
                organizationId={organizationId}
                open={addOpen || !!editing}
                onOpenChange={(v) => {
                  if (!v) {
                    setAddOpen(false)
                    setEditing(undefined)
                  }
                }}
                editing={editing}
              />
            )}
            {rotating && (
              <RotateSecretPanel
                organizationId={organizationId}
                webhookId={rotating}
                onOpenChange={(v) => !v && setRotating(null)}
              />
            )}
          </div>
        </div>
      </PermissionGuard>
    </OverlayPanel>
  )
}
