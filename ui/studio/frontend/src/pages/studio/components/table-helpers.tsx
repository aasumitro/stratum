import { IconAlertTriangle } from "@tabler/icons-react"

export function WriteBanner() {
  return (
    <div className="flex items-start gap-2.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
      <IconAlertTriangle className="mt-0.5 size-4 shrink-0" />
      <span>
        This modifies <strong>production data</strong> directly. Changes take
        effect immediately.
      </span>
    </div>
  )
}

export function TableSkeleton({ cols }: { cols: number }) {
  return (
    <div className="animate-pulse">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 border-b px-4 py-3">
          {Array.from({ length: cols }).map((__, j) => (
            <div key={j} className="h-4 flex-1 rounded bg-muted" />
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
      Amounts are integers in the currency's smallest unit — multiply the real
      price by <code className="font-mono">10^decimal_places</code> for that
      currency (see References → Currencies). <strong>USD</strong> has 2 decimal
      places (cents): $9.00 → <code className="font-mono">900</code>, $49.00 →{" "}
      <code className="font-mono">4900</code>. <strong>IDR</strong> has 0
      decimal places (no cents, whole Rupiah): Rp 13.500 →{" "}
      <code className="font-mono">13500</code> — do not multiply by 100 for IDR.
    </FieldHint>
  )
}
