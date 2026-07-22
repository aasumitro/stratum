import { IconAlertTriangle } from "@tabler/icons-react"

export function WriteBanner() {
  return (
    <div className="flex items-start gap-2.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
      <IconAlertTriangle className="size-4 mt-0.5 shrink-0" />
      <span>
        This modifies <strong>production data</strong> directly. Changes take effect immediately.
      </span>
    </div>
  )
}

export function TableSkeleton({ cols }: { cols: number }) {
  return (
    <div className="animate-pulse">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 px-4 py-3 border-b">
          {Array.from({ length: cols }).map((__, j) => (
            <div key={j} className="h-4 bg-muted rounded flex-1" />
          ))}
        </div>
      ))}
    </div>
  )
}

export function FieldHint({ children }: { children: React.ReactNode }) {
  return <p className="text-xs text-muted-foreground">{children}</p>
}

// Explains how amounts map to stored integers: multiply the real price by
// 10^decimal_places for that currency (2 for USD → cents, 0 for IDR → whole
// Rupiah, no multiplication) — the recurring point of confusion is that
// "smallest unit" isn't always cents, and the multiplier differs per currency.
export function PriceUnitHint() {
  return (
    <FieldHint>
      Amounts are integers in the currency's smallest unit — multiply the real price by{" "}
      <code className="font-mono">10^decimal_places</code> for that currency (see References → Currencies).{" "}
      <strong>USD</strong> has 2 decimal places (cents): $9.00 → <code className="font-mono">900</code>, $49.00 → <code className="font-mono">4900</code>.{" "}
      <strong>IDR</strong> has 0 decimal places (no cents, whole Rupiah): Rp 13.500 → <code className="font-mono">13500</code> — do not multiply by 100 for IDR.
    </FieldHint>
  )
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—"
  return new Date(iso).toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" })
}

export function formatJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

// Converts a stored UTC ISO string (e.g. from the Go backend) into the local
// wall-clock "YYYY-MM-DDTHH:mm" format <input type="datetime-local"> expects.
// A datetime-local value carries no timezone — the browser always treats it
// as local time. Slicing the raw UTC ISO string instead of converting it
// makes the displayed value drift from what was typed (and can even show a
// different calendar day) for anyone not in UTC.
export function toDatetimeLocalValue(iso: string): string {
  if (!iso) return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function buildPricesStub(currencyCodes: string[]): string {
  const stub: Record<string, { monthly: number; yearly: number }> = {}
  for (const code of currencyCodes.length > 0 ? currencyCodes : ["USD"]) {
    stub[code] = { monthly: 0, yearly: 0 }
  }
  return JSON.stringify(stub, null, 2)
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B"
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// Metered features whose metric_key ends in "_bytes" (currently just
// storage_bytes) should render their limit_value through formatBytes instead
// of as a raw integer — otherwise an operator (or a future customer-facing
// UI reading the same API) sees "10737418240" instead of "10 GB". Extend
// this if a future metric introduces a different non-obvious unit.
export function formatMetricValue(value: number, metricKey: string | null | undefined): string {
  if (value === -1) return "unlimited"
  if (metricKey?.endsWith("_bytes")) return formatBytes(value)
  return String(value)
}
