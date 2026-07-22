import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  IconWebhook,
  IconRefresh,
  IconLoader2,
  IconCopy,
} from "@tabler/icons-react"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs"
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn } from "@/components/shared/data-table"
import { DataTablePagination } from "@/components/shared/pagination"
import { FilterBar, type FilterChip } from "@/components/shared/filter-bar"
import { SideDrawer } from "@/components/shared/side-drawer"
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

      <SideDrawer
        open={!!detail}
        onOpenChange={(open) => !open && setDetail(null)}
        title={t("organization.webhooks.deliveryDetail")}
        description={detail?.event_type}
        footer={
          detail && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => copyCurl(detail)}
            >
              <IconCopy data-icon="inline-start" />
              {t("organization.webhooks.copyCurl")}
            </Button>
          )
        }
      >
        {detail && (
          <div className="flex flex-col gap-4 py-2 text-sm">
            <div>
              <p className="text-xs font-semibold text-muted-foreground">
                {t("organization.webhooks.attemptsTimeline")}
              </p>
              <div className="mt-1 flex flex-col gap-1">
                {(detail.attempts_log ?? []).map((a) => (
                  <div
                    key={a.attempt}
                    className="flex items-center justify-between rounded-md border px-2 py-1 text-xs"
                  >
                    <span>
                      #{a.attempt} · {new Date(a.attempted_at).toLocaleString()}
                    </span>
                    <span
                      className={
                        a.error ? "text-destructive" : "text-emerald-600"
                      }
                    >
                      {a.error ?? `HTTP ${a.status_code}`}
                      {a.latency_ms != null && ` · ${a.latency_ms}ms`}
                    </span>
                  </div>
                ))}
              </div>
            </div>

            <Tabs defaultValue="request">
              <TabsList className="w-full">
                <TabsTrigger value="request">
                  {t("organization.webhooks.tabRequest")}
                </TabsTrigger>
                <TabsTrigger value="headers">
                  {t("organization.webhooks.tabHeaders")}
                </TabsTrigger>
                <TabsTrigger value="response">
                  {t("organization.webhooks.tabResponse")}
                </TabsTrigger>
              </TabsList>
              <TabsContent value="request">
                <pre className="overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                  {JSON.stringify(
                    { id: detail.event_id, type: detail.event_type },
                    null,
                    2
                  )}
                </pre>
              </TabsContent>
              <TabsContent value="headers">
                <pre className="overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                  {`Content-Type: application/json\nX-Stratum-Event: ${detail.event_type}\nX-Stratum-Signature: t=<unix_ts>,v1=<hmac_sha256>`}
                </pre>
              </TabsContent>
              <TabsContent value="response">
                {detail.response_status_code != null || detail.response_body ? (
                  <pre className="overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                    {`HTTP ${detail.response_status_code ?? "—"}\n\n${detail.response_body ?? ""}`}
                  </pre>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    {t("organization.webhooks.noResponse")}
                  </p>
                )}
              </TabsContent>
            </Tabs>
          </div>
        )}
      </SideDrawer>
    </div>
  )
}
