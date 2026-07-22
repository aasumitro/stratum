import type { RuntimeStats } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { formatBytes } from "../components/table-helpers"
import { formatUptime } from "./utils"

function StatCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-lg border bg-card px-4 py-3">
      <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">{label}</span>
      <span className="text-lg font-semibold tabular-nums leading-tight">{value}</span>
      {sub && <span className="text-[10px] text-muted-foreground">{sub}</span>}
    </div>
  )
}

export function StatsGrid({ stats }: { stats: RuntimeStats }) {
  const hitTotal = (stats.redis_pool?.hits ?? 0) + (stats.redis_pool?.misses ?? 0)
  const hitRate = hitTotal > 0
    ? `${((stats.redis_pool!.hits / hitTotal) * 100).toFixed(1)}% hit`
    : null

  return (
    <div className="flex flex-col gap-3">
      <h2 className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
        Runtime metrics
      </h2>

      {/* Process */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
        <StatCard
          label="Uptime"
          value={formatUptime(stats.uptime_seconds)}
        />
        <StatCard
          label="Goroutines"
          value={String(stats.goroutines)}
        />
        <StatCard
          label="CPUs"
          value={String(stats.num_cpu)}
        />
        <StatCard
          label="GC runs"
          value={String(stats.memory.gc_runs)}
          sub={`${(stats.memory.gc_cpu_fraction * 100).toFixed(2)}% CPU`}
        />
      </div>

      {/* Memory */}
      <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
        <StatCard
          label="Heap alloc"
          value={formatBytes(stats.memory.heap_alloc_bytes)}
          sub="live objects"
        />
        <StatCard
          label="Heap sys"
          value={formatBytes(stats.memory.heap_sys_bytes)}
          sub="from OS"
        />
        <StatCard
          label="Stack"
          value={formatBytes(stats.memory.stack_in_use_bytes)}
          sub="in use"
        />
      </div>

      {/* DB pool */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
        <StatCard
          label="DB acquired"
          value={`${stats.db_pool.acquired_conns} / ${stats.db_pool.max_conns}`}
          sub="conns in use"
        />
        <StatCard
          label="DB idle"
          value={String(stats.db_pool.idle_conns)}
          sub="connections"
        />
        <StatCard
          label="DB total"
          value={String(stats.db_pool.total_conns)}
          sub="in pool"
        />
        {stats.redis_pool && (
          <StatCard
            label="Redis pool"
            value={`${stats.redis_pool.idle_conns} / ${stats.redis_pool.total_conns}`}
            sub={hitRate ?? "idle / total"}
          />
        )}
      </div>
    </div>
  )
}
