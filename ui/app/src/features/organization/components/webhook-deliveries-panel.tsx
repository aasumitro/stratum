import type { ReactNode, ElementType } from "react"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  IconWebhook,
  IconRefresh,
  IconLoader2,
  IconCopy,
  IconX,
  IconId,
  IconClock,
  IconHistory,
  IconCode,
} from "@tabler/icons-react"
import { cn } from "@/lib/ui"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn } from "@/components/shared/data-table"
import { DataTablePagination } from "@/components/shared/pagination"
import { FilterBar, type FilterChip } from "@/components/shared/filter-bar"
import { StatusBadge } from "@/components/shared/status-badge"
import { useCursorAccumulator } from "@/lib/api/use-cursor-accumulator"
import {
  useWebhookDeliveries,
  useRetryDelivery,
  useRetryAllFailedDeliveries,
  type WebhookDeliveryFilter,
} from "@/features/organization/hooks"
import type { WebhookDelivery, WebhookEndpoint } from "@/types/organization"

const STATUSES = ["delivered", "failed", "pending"] as const

function buildCurl(
  endpoint: WebhookEndpoint,
  delivery: WebhookDelivery
): string {
  const body = JSON.stringify({
    id: delivery.event_id,
    type: delivery.event_type,
  })
  return [
    `curl -X POST '${endpoint.url}'`,
    `  -H 'Content-Type: application/json'`,
    `  -H 'X-Stratum-Event: ${delivery.event_type}'`,
    `  -H 'X-Stratum-Signature: t=<unix_ts>,v1=<hmac_sha256>'`,
    `  -d '${body}'`,
  ].join(" \\\n")
}

function DetailField({
  icon: Icon,
  label,
  children,
}: {
  icon: ElementType
  label: string
  children: ReactNode
}) {
  return (
    <div className="flex gap-3">
      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <p className="text-xs font-medium text-muted-foreground">{label}</p>
        <div className="mt-0.5 text-sm">{children}</div>
      </div>
    </div>
  )
}

interface Props {
  organizationId: string
  endpoint: WebhookEndpoint
}

// Filterable delivery history, attempt timeline (from attempts_log),
// Request/Headers/Response detail tabs, single + bulk retry, Copy as cURL.
// The original outbound payload isn't retained server-side, so the Request
// tab shows the same reconstructed minimal envelope a retry actually sends,
// not a stored original.
export function WebhookDeliveriesPanel({ organizationId, endpoint }: Props) {
  const { t } = useTranslation()
  const [filter, setFilter] = useState<WebhookDeliveryFilter>({})
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [detail, setDetail] = useState<WebhookDelivery | null>(null)

  const { data, isLoading, isError, isFetching, refetch } =
    useWebhookDeliveries(organizationId, endpoint.id, filter, cursor)
  const { items: deliveries, nextCursor } =
    useCursorAccumulator<WebhookDelivery>(data, cursor)

  function updateFilter(next: WebhookDeliveryFilter) {
    setCursor(undefined)
    setFilter(next)
  }

  const { mutate: retry, isPending: retrying } = useRetryDelivery(
    organizationId,
    endpoint.id
  )
  const { mutate: retryAll, isPending: retryingAll } =
    useRetryAllFailedDeliveries(organizationId, endpoint.id)

  const chips: FilterChip[] = []
  if (filter.status) {
    chips.push({
      key: "status",
      label: `${t("organization.webhooks.statusCol")}: ${filter.status}`,
      onRemove: () => updateFilter({ ...filter, status: undefined }),
    })
  }
  if (filter.event_type) {
    chips.push({
      key: "event_type",
      label: `${t("organization.webhooks.eventTypeCol")}: ${filter.event_type}`,
      onRemove: () => updateFilter({ ...filter, event_type: undefined }),
    })
  }

  function copyCurl(delivery: WebhookDelivery) {
    void navigator.clipboard.writeText(buildCurl(endpoint, delivery))
    toast.success(t("organization.webhooks.curlCopied"))
  }

  const columns: DataTableColumn<WebhookDelivery>[] = [
    {
      key: "event_type",
      header: t("organization.webhooks.eventTypeCol"),
      className: "font-mono text-xs",
      cell: (d) => d.event_type,
    },
    {
      key: "status",
      header: t("organization.webhooks.statusCol"),
      cell: (d) => <StatusBadge status={d.status} />,
    },
    {
      key: "attempts",
      header: t("organization.webhooks.attemptsCol"),
      cell: (d) => d.attempts,
    },
    {
      key: "latency",
      header: t("organization.webhooks.latencyCol"),
      className: "text-xs text-muted-foreground",
      cell: (d) => (d.latency_ms != null ? `${d.latency_ms}ms` : "—"),
    },
    {
      key: "when",
      header: t("organization.webhooks.deliveredAtCol"),
      className: "text-xs whitespace-nowrap text-muted-foreground",
      cell: (d) =>
        d.delivered_at
          ? new Date(d.delivered_at).toLocaleString()
          : new Date(d.created_at).toLocaleString(),
    },
    {
      key: "actions",
      header: "",
      hideOnMobile: true,
      cell: (d) =>
        d.status === "failed" && (
          <Button
            size="sm"
            variant="ghost"
            onClick={(e) => {
              e.stopPropagation()
              retry(d.id)
            }}
            disabled={retrying}
          >
            {retrying ? (
              <IconLoader2 className="size-3 animate-spin" />
            ) : (
              <IconRefresh className="size-3" />
            )}
          </Button>
        ),
    },
  ]

  return (
    <div className="mt-4 flex flex-col gap-3">
      <FilterBar chips={chips} onClearAll={() => updateFilter({})}>
        <Select
          value={filter.status ?? "all"}
          onValueChange={(v) =>
            updateFilter({
              ...filter,
              status: !v || v === "all" ? undefined : v,
            })
          }
        >
          <SelectTrigger className="h-8 w-32 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">
              {t("organization.webhooks.allStatuses")}
            </SelectItem>
            {STATUSES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <div className="ml-auto">
          <Button
            variant="outline"
            size="sm"
            disabled={retryingAll}
            onClick={() => retryAll()}
          >
            {retryingAll && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {t("organization.webhooks.retryAllFailed")}
          </Button>
        </div>
      </FilterBar>

      <DataTable
        columns={columns}
        rows={deliveries}
        rowKey={(d) => d.id}
        isLoading={isLoading}
        isError={isError}
        onRetry={() => void refetch()}
        onRowClick={setDetail}
        skeletonRows={3}
        empty={{
          icon: IconWebhook,
          title: t("organization.webhooks.noDeliveries"),
        }}
      />

      <DataTablePagination
        mode="cursor"
        hasMore={!!nextCursor}
        isLoadingMore={isFetching}
        onLoadMore={() => setCursor(nextCursor)}
        loadMoreLabel={t("common.loadMore")}
      />

      {detail && (
        <button
          type="button"
          aria-label={t("common.close")}
          className="absolute inset-0 z-10 bg-black/20"
          onClick={() => setDetail(null)}
        />
      )}
      <div
        className={cn(
          "absolute inset-y-0 right-0 z-20 flex w-full flex-col border-l bg-popover transition-transform duration-200 sm:w-1/2",
          detail ? "translate-x-0 shadow-xl" : "translate-x-full shadow-none"
        )}
      >
        {detail && (
          <>
            <div className="flex shrink-0 items-start justify-between gap-3 border-b px-4 py-3">
              <div className="min-w-0">
                <p className="font-heading text-sm font-medium">
                  {t("organization.webhooks.deliveryDetail")}
                </p>
                <div className="mt-1.5">
                  <StatusBadge
                    status={
                      detail.status === "delivered"
                        ? "active"
                        : detail.status === "pending"
                          ? "pending"
                          : "failed"
                    }
                    label={detail.event_type}
                  />
                </div>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t("common.close")}
                onClick={() => setDetail(null)}
              >
                <IconX className="size-4" />
              </Button>
            </div>
            <div className="flex-1 overflow-y-auto px-4 py-4">
              <div className="flex flex-col gap-4">
                <DetailField
                  icon={IconId}
                  label={t("organization.webhooks.eventId")}
                >
                  <span className="font-mono text-xs break-all text-muted-foreground">
                    {detail.event_id}
                  </span>
                </DetailField>

                <DetailField
                  icon={IconClock}
                  label={t("organization.webhooks.createdAt")}
                >
                  {new Date(detail.created_at).toLocaleString()}
                </DetailField>

                <DetailField
                  icon={IconHistory}
                  label={t("organization.webhooks.attemptsTimeline")}
                >
                  <div className="mt-1 flex flex-col gap-1">
                    {(detail.attempts_log ?? []).map((a) => (
                      <div
                        key={a.attempt}
                        className="flex items-center justify-between rounded-md border px-2 py-1.5 text-[11px]"
                      >
                        <span className="font-medium">
                          #{a.attempt} ·{" "}
                          <span className="font-normal text-muted-foreground">
                            {new Date(a.attempted_at).toLocaleString()}
                          </span>
                        </span>
                        <span
                          className={
                            a.error
                              ? "text-destructive"
                              : "font-medium text-emerald-600"
                          }
                        >
                          {a.error ?? `HTTP ${a.status_code}`}
                          {a.latency_ms != null && ` · ${a.latency_ms}ms`}
                        </span>
                      </div>
                    ))}
                  </div>
                </DetailField>

                <DetailField
                  icon={IconCode}
                  label={t("organization.webhooks.payload")}
                >
                  <div className="mt-2 flex flex-col gap-4">
                    <div className="flex flex-col gap-1.5">
                      <p className="text-[10px] font-semibold tracking-wider text-muted-foreground uppercase">
                        {t("organization.webhooks.tabHeaders")}
                      </p>
                      <pre className="overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                        {`Content-Type: application/json\nX-Stratum-Event: ${detail.event_type}\nX-Stratum-Signature: t=<unix_ts>,v1=<hmac_sha256>`}
                      </pre>
                    </div>

                    <div className="flex flex-col gap-1.5">
                      <p className="text-[10px] font-semibold tracking-wider text-muted-foreground uppercase">
                        {t("organization.webhooks.tabRequest")}
                      </p>
                      <pre className="overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                        {JSON.stringify(
                          { id: detail.event_id, type: detail.event_type },
                          null,
                          2
                        )}
                      </pre>
                    </div>

                    <div className="flex flex-col gap-1.5">
                      <p className="text-[10px] font-semibold tracking-wider text-muted-foreground uppercase">
                        {t("organization.webhooks.tabResponse")}
                      </p>
                      {detail.response_status_code != null ||
                      detail.response_body ? (
                        <pre className="overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                          {`HTTP ${detail.response_status_code ?? "—"}\n\n${detail.response_body ?? ""}`}
                        </pre>
                      ) : (
                        <p className="text-xs text-muted-foreground">
                          {t("organization.webhooks.noResponse")}
                        </p>
                      )}
                    </div>
                  </div>
                </DetailField>
              </div>
            </div>
            <div className="flex shrink-0 justify-end border-t px-4 py-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => copyCurl(detail)}
              >
                <IconCopy data-icon="inline-start" />
                {t("organization.webhooks.copyCurl")}
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
