import {
  IconBuildingSkyscraper,
  IconDatabase,
  IconTrendingUp,
  IconUsers,
  IconCurrencyDollar,
  IconCalendarPlus,
} from "@tabler/icons-react"
import type {
  PlanCount,
  ProjectMetrics,
  SubscriptionStatus,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { Badge } from "@/components/ui/badge"
import { Card } from "@/components/ui/card"
import { formatBytes } from "../components/table-helpers"
import { formatMoney, STATUS_COLORS } from "./utils"
import { MetricCard } from "./metric-card"

export function DashboardContent({ metrics }: { metrics: ProjectMetrics }) {
  const activeSubscriptions =
    metrics.subscriptions_by_status
      ?.filter((s: SubscriptionStatus) => s.status === "active" || s.status === "trialing")
      .reduce((acc: number, s: SubscriptionStatus) => acc + s.count, 0) ?? 0

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Row 1 — core metrics */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <MetricCard
          icon={<IconBuildingSkyscraper className="size-5" />}
          label="Total Organizations"
          value={String(metrics.total_organizations)}
          sub={`${metrics.active_organizations} active`}
        />
        <MetricCard
          icon={<IconUsers className="size-5" />}
          label="Total Members"
          value={String(metrics.total_members)}
        />
        <MetricCard
          icon={<IconCurrencyDollar className="size-5" />}
          label="MRR"
          value={formatMoney(metrics.mrr)}
          sub={`${formatMoney(metrics.arr)} ARR`}
        />
        <MetricCard
          icon={<IconTrendingUp className="size-5" />}
          label="Active Subscriptions"
          value={String(activeSubscriptions)}
          sub={`across all statuses`}
        />
      </div>

      {/* Row 2 — growth + storage */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <MetricCard
          icon={<IconCalendarPlus className="size-5" />}
          label="New Organizations (30d)"
          value={String(metrics.new_organizations_last_30d)}
        />
        <MetricCard
          icon={<IconDatabase className="size-5" />}
          label="Storage Used"
          value={formatBytes(metrics.storage_bytes_total)}
        />
        <Card className="p-5">
          <span className="text-xs text-muted-foreground font-medium block mb-3">
            Subscriptions by Status
          </span>
          {metrics.subscriptions_by_status?.length ? (
            <div className="flex flex-wrap gap-2">
              {metrics.subscriptions_by_status.map((s: SubscriptionStatus) => (
                <span
                  key={s.status}
                  className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ${STATUS_COLORS[s.status] ?? "bg-muted text-muted-foreground"}`}
                >
                  {s.status}
                  <span className="font-semibold tabular-nums">{s.count}</span>
                </span>
              ))}
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">No subscriptions yet</p>
          )}
        </Card>
      </div>

      {/* Row 3 — top plans */}
      {metrics.top_plans_by_organization?.length ? (
        <Card className="p-5">
          <span className="text-xs text-muted-foreground font-medium block mb-3">
            Top Plans by Active Organizations
          </span>
          <div className="flex flex-col gap-2">
            {metrics.top_plans_by_organization.map((p: PlanCount, i: number) => (
              <div key={p.plan} className="flex items-center gap-3">
                <span className="text-xs text-muted-foreground w-4 tabular-nums">
                  {i + 1}.
                </span>
                <span className="flex-1 text-sm font-medium capitalize">{p.plan}</span>
                <Badge variant="secondary" className="tabular-nums">
                  {p.count} organization{p.count !== 1 ? "s" : ""}
                </Badge>
              </div>
            ))}
          </div>
        </Card>
      ) : null}
    </div>
  )
}
