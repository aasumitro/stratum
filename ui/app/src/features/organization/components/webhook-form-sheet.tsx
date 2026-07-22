import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import { SideDrawer } from "@/components/shared/side-drawer"
import { UnsavedChangesGuard } from "@/components/shared/unsaved-changes-guard"
import {
  useCreateWebhook,
  useUpdateWebhook,
} from "@/features/organization/hooks"
import {
  WEBHOOK_EVENT_CATALOG,
  WEBHOOK_EVENT_GROUPS,
} from "@/features/organization/webhook-events-catalog"
import type { WebhookEndpoint } from "@/types/organization"

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (v: boolean) => void
  editing?: WebhookEndpoint
}

// Per-event subscriptions. subscribedEvents.length === 0 means "all
// events" (empty = allow all, same convention as the IP allowlist) — the
// checkbox list is only shown once the caller opts out of "all events".
export function WebhookFormSheet({
  organizationId,
  open,
  onOpenChange,
  editing,
}: Props) {
  const { t } = useTranslation()
  const [url, setUrl] = useState(editing?.url ?? "")
  const [enabled, setEnabled] = useState(editing?.enabled ?? true)
  const [allEvents, setAllEvents] = useState(
    !editing?.subscribed_events?.length
  )
  const [subscribedEvents, setSubscribedEvents] = useState<string[]>(
    editing?.subscribed_events ?? []
  )
  const [secret, setSecret] = useState<string | null>(null)
  const [secretStored, setSecretStored] = useState(false)

  const { mutate: create, isPending: creating } =
    useCreateWebhook(organizationId)
  const { mutate: update, isPending: updating } =
    useUpdateWebhook(organizationId)
  const isPending = creating || updating

  function toggleEvent(key: string, checked: boolean) {
    setSubscribedEvents((prev) =>
      checked ? [...prev, key] : prev.filter((k) => k !== key)
    )
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!url.trim()) return
    const events = allEvents ? [] : subscribedEvents

    if (editing) {
      update(
        { id: editing.id, url, enabled, subscribed_events: events },
        { onSuccess: () => onOpenChange(false) }
      )
    } else {
      create(
        { url, subscribed_events: events },
        {
          onSuccess: (res) => {
            if (res.data) setSecret(res.data.secret)
          },
        }
      )
    }
  }

  function handleClose() {
    setUrl(editing?.url ?? "")
    setEnabled(editing?.enabled ?? true)
    setAllEvents(!editing?.subscribed_events?.length)
    setSubscribedEvents(editing?.subscribed_events ?? [])
    setSecret(null)
    setSecretStored(false)
    onOpenChange(false)
  }

  const isDirty =
    open &&
    !secret &&
    (url !== (editing?.url ?? "") ||
      enabled !== (editing?.enabled ?? true) ||
      allEvents !== !editing?.subscribed_events?.length ||
      JSON.stringify([...subscribedEvents].sort()) !==
        JSON.stringify([...(editing?.subscribed_events ?? [])].sort()))

  return (
    <>
      <UnsavedChangesGuard isDirty={isDirty} />
      <SideDrawer
        open={open}
        onOpenChange={handleClose}
        title={
          editing
            ? t("organization.webhooks.editTitle")
            : t("organization.webhooks.addTitle")
        }
        description={t("organization.webhooks.formDescription")}
      >
        <form onSubmit={handleSubmit} className="flex flex-col gap-4 py-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="wh-url">
              {t("organization.webhooks.urlLabel")}
            </Label>
            <Input
              id="wh-url"
              type="url"
              placeholder="https://example.com/webhook"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              disabled={!!secret}
              required
            />
          </div>

          {!secret && (
            <div className="flex flex-col gap-2">
              <Label>{t("organization.webhooks.eventsLabel")}</Label>
              <div className="flex items-start gap-2">
                <Checkbox
                  id="wh-all-events"
                  checked={allEvents}
                  onCheckedChange={(v) => setAllEvents(v === true)}
                />
                <Label htmlFor="wh-all-events" className="text-sm font-normal">
                  {t("organization.webhooks.allEvents")}
                </Label>
              </div>
              {!allEvents && (
                <div className="flex flex-col gap-3 rounded-lg border p-3">
                  {WEBHOOK_EVENT_GROUPS.map((group) => (
                    <div key={group} className="flex flex-col gap-1.5">
                      <p className="text-xs font-semibold text-muted-foreground">
                        {t(`organization.webhooks.eventGroup.${group}`)}
                      </p>
                      {WEBHOOK_EVENT_CATALOG.filter(
                        (e) => e.group === group
                      ).map((e) => (
                        <div key={e.key} className="flex items-center gap-2">
                          <Checkbox
                            id={`wh-event-${e.key}`}
                            checked={subscribedEvents.includes(e.key)}
                            onCheckedChange={(v) =>
                              toggleEvent(e.key, v === true)
                            }
                          />
                          <Label
                            htmlFor={`wh-event-${e.key}`}
                            className="font-mono text-xs font-normal"
                          >
                            {e.key}
                          </Label>
                        </div>
                      ))}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {editing && !secret && (
            <div className="flex items-center gap-3">
              <Checkbox
                id="wh-enabled"
                checked={enabled}
                onCheckedChange={(v) => setEnabled(v === true)}
                disabled={!!editing.auto_disabled_at}
              />
              <Label htmlFor="wh-enabled" className="font-normal">
                {t("organization.webhooks.enabledLabel")}
              </Label>
              {editing.auto_disabled_at && (
                <span className="text-xs text-muted-foreground">
                  {t("organization.webhooks.autoDisabledHint")}
                </span>
              )}
            </div>
          )}

          {secret && (
            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-1 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3">
                <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
                  {t("organization.webhooks.secretWarning")}
                </p>
                <code className="font-mono text-xs break-all select-all">
                  {secret}
                </code>
              </div>
              <div className="flex items-start gap-2">
                <Checkbox
                  id="wh-secret-stored"
                  checked={secretStored}
                  onCheckedChange={(v) => setSecretStored(v === true)}
                />
                <Label
                  htmlFor="wh-secret-stored"
                  className="text-sm font-normal"
                >
                  {t("organization.webhooks.secretStoredAck")}
                </Label>
              </div>
            </div>
          )}

          <div className="flex gap-2">
            {!secret && (
              <Button type="submit" disabled={isPending || !url.trim()}>
                {isPending && (
                  <IconLoader2
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                )}
                {editing
                  ? t("organization.webhooks.save")
                  : t("organization.webhooks.add")}
              </Button>
            )}
            <Button
              type="button"
              variant="outline"
              onClick={handleClose}
              disabled={!!secret && !secretStored}
            >
              {secret ? t("common.done") : t("common.cancel")}
            </Button>
          </div>
        </form>
      </SideDrawer>
    </>
  )
}
