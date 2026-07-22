import { useState } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  IconPlus,
  IconDotsVertical,
  IconChevronDown,
  IconChevronRight,
  IconWebhook,
  IconLock,
  IconAlertTriangle,
} from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { EmptyState } from "@/components/shared/empty-state"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"
import { PermissionGuard } from "@/components/shared/permission-guard"
import {
  useWebhooks,
  useDeleteWebhook,
  useSendWebhookTestEvent,
} from "@/features/organization/hooks"
import { useBillingFeatures } from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { WebhookFormSheet } from "@/features/organization/components/webhook-form-sheet"
import { RotateSecretDialog } from "@/features/organization/components/rotate-secret-dialog"
import { WebhookDeliveriesPanel } from "@/features/organization/components/webhook-deliveries-panel"
import { WebhookSparkline } from "@/features/organization/components/webhook-sparkline"
import type { WebhookEndpoint } from "@/types/organization"

const WEBHOOK_FEATURE_ID = "webhooks"

function healthTone(percent: number): string {
  if (percent >= 90)
    return "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
  if (percent >= 70) return "bg-amber-500/10 text-amber-600 dark:text-amber-400"
  return "bg-destructive/10 text-destructive"
}

export function WebhooksPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { canAccessWebhooks } = usePermissions()

  const { data: featuresData } = useBillingFeatures(organizationId)
  const hasWebhooksFeature = (featuresData?.data ?? []).some(
    (f) => f.feature_id === WEBHOOK_FEATURE_ID
  )

  const { data, isLoading } = useWebhooks(organizationId, canAccessWebhooks)
  const { mutate: deleteWebhook, isPending: deleting } =
    useDeleteWebhook(organizationId)
  const { mutate: sendTestEvent, isPending: sendingTest } =
    useSendWebhookTestEvent(organizationId)
  const [addOpen, setAddOpen] = useState(false)
  const [editing, setEditing] = useState<WebhookEndpoint | undefined>()
  const [rotating, setRotating] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<WebhookEndpoint | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const endpoints = data?.data ?? []

  function toggleExpand(id: string) {
    setExpanded((prev) => (prev === id ? null : id))
  }

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
    <PermissionGuard
      allowed={canAccessWebhooks}
      explainer={{
        title: t("organization.webhooks.deniedTitle"),
        description: t("organization.webhooks.deniedDescription"),
      }}
    >
      <div className="flex flex-col gap-6">
        <WebhookFormSheet
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
        <RotateSecretDialog
          organizationId={organizationId}
          webhookId={rotating}
          onOpenChange={(v) => !v && setRotating(null)}
        />

        <Card>
          <CardHeader className="flex flex-row items-start justify-between">
            <div>
              <CardTitle>{t("organization.webhooks.title")}</CardTitle>
              <CardDescription className="mt-1">
                {t("organization.webhooks.description")}
              </CardDescription>
            </div>
            {hasWebhooksFeature && (
              <Button size="sm" onClick={() => setAddOpen(true)}>
                <IconPlus data-icon="inline-start" />
                {t("organization.webhooks.add")}
              </Button>
            )}
          </CardHeader>
          <CardContent>
            {isLoading ? (
              <div className="flex flex-col gap-3">
                {[1, 2].map((i) => (
                  <Skeleton key={i} className="h-12 w-full" />
                ))}
              </div>
            ) : !hasWebhooksFeature ? (
              <EmptyState
                icon={IconLock}
                title={t("organization.webhooks.upgradeTitle")}
                description={t("organization.webhooks.upgradeDescription")}
                action={{
                  label: t("organization.webhooks.upgradeCta"),
                  onClick: () =>
                    void navigate({
                      to: "/organization/$organizationId/billing",
                      params: { organizationId },
                    }),
                }}
              />
            ) : !endpoints.length ? (
              <EmptyState
                icon={IconWebhook}
                title={t("organization.webhooks.noEndpoints")}
                action={{
                  label: t("organization.webhooks.add"),
                  onClick: () => setAddOpen(true),
                }}
              />
            ) : (
              <div className="flex flex-col divide-y">
                {endpoints.map((ep) => {
                  const percent = ep.health?.success_percent_24h ?? 100
                  return (
                    <div key={ep.id} className="py-3">
                      <div className="flex items-center gap-3">
                        <button
                          onClick={() => toggleExpand(ep.id)}
                          className="text-muted-foreground transition-colors hover:text-foreground"
                          aria-label={t(
                            "organization.webhooks.toggleDeliveries"
                          )}
                        >
                          {expanded === ep.id ? (
                            <IconChevronDown className="size-4" />
                          ) : (
                            <IconChevronRight className="size-4" />
                          )}
                        </button>

                        <span
                          className={`rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap ${healthTone(percent)}`}
                        >
                          {percent}%
                        </span>

                        <WebhookSparkline
                          organizationId={organizationId}
                          webhookId={ep.id}
                        />

                        <span className="flex-1 truncate font-mono text-sm">
                          {ep.url}
                        </span>

                        {ep.auto_disabled_at ? (
                          <span className="flex items-center gap-1 text-xs text-destructive">
                            <IconAlertTriangle className="size-3.5" />
                            {t("organization.webhooks.autoDisabled")}
                          </span>
                        ) : (
                          <span className="text-xs text-muted-foreground">
                            {ep.enabled
                              ? t("organization.webhooks.enabledLabel")
                              : t("organization.webhooks.disabledLabel")}
                          </span>
                        )}

                        <DropdownMenu>
                          <DropdownMenuTrigger
                            render={
                              <Button
                                size="icon"
                                variant="ghost"
                                aria-label={t("organization.webhooks.actions")}
                              />
                            }
                          >
                            <IconDotsVertical className="size-4" />
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => setEditing(ep)}>
                              {t("organization.webhooks.edit")}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => setRotating(ep.id)}
                            >
                              {t("organization.webhooks.rotateSecret")}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              disabled={sendingTest}
                              onClick={() => handleTestEvent(ep.id)}
                            >
                              {ep.auto_disabled_at
                                ? t("organization.webhooks.sendTestToReenable")
                                : t("organization.webhooks.sendTestEvent")}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              variant="destructive"
                              onClick={() => setDeleteTarget(ep)}
                            >
                              {t("organization.webhooks.delete")}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>

                      {expanded === ep.id && (
                        <WebhookDeliveriesPanel
                          organizationId={organizationId}
                          endpoint={ep}
                        />
                      )}
                    </div>
                  )
                })}
              </div>
            )}
          </CardContent>
        </Card>

        {deleteTarget && (
          <ConfirmationDialog
            open
            onOpenChange={(open) => !open && setDeleteTarget(null)}
            render={<span className="hidden" />}
            nativeButton={false}
            title={t("organization.webhooks.deleteTitle")}
            description={t("organization.webhooks.deleteDescription")}
            confirmLabel={t("organization.webhooks.delete")}
            pending={deleting}
            onConfirm={() =>
              deleteWebhook(deleteTarget.id, {
                onSuccess: () => setDeleteTarget(null),
              })
            }
          >
            {null}
          </ConfirmationDialog>
        )}
      </div>
    </PermissionGuard>
  )
}
