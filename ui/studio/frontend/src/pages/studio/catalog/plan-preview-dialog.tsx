import { useState } from "react"
import { IconCheck } from "@tabler/icons-react"
import type {
  Currency,
  Feature,
  Plan,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { usePlanFeatures } from "@/hooks/use-catalog"
import { useCurrencies } from "@/hooks/use-references"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { formatMetricValue } from "../components/table-helpers"
import { formatMoney } from "./save-confirm-dialog"

type Cycle = "monthly" | "yearly"

// Config-type entitlements store an arbitrary JSON blob (e.g. rate limits) —
// a marketing page would never show that raw, so render it as "key: value"
// copy instead of the braces a customer has no reason to parse.
function formatConfigValue(raw: string): string {
  try {
    const parsed = JSON.parse(raw)
    if (parsed && typeof parsed === "object") {
      return Object.entries(parsed).map(([k, v]) => `${k}: ${v}`).join(", ")
    }
    return String(parsed)
  } catch {
    return raw
  }
}

function PlanPreviewEntitlements({
  projectId, planId, features,
}: { projectId: string; planId: string; features: Feature[] | null | undefined }) {
  const { data: entitlements, isLoading } = usePlanFeatures(projectId, planId)
  if (isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>
  if (!entitlements || entitlements.length === 0) {
    return <p className="text-sm text-muted-foreground">No entitlements yet</p>
  }
  return (
    <ul className="flex flex-col gap-2.5">
      {entitlements.map((e) => {
        const f = features?.find((x) => x.id === e.feature_id)
        const label = f?.name ?? e.feature_id
        const detail =
          e.limit_value !== null
            ? formatMetricValue(e.limit_value, f?.metric_key)
            : f?.type === "config" && e.config_value
              ? formatConfigValue(e.config_value)
              : undefined
        return (
          <li key={e.feature_id} className="flex items-start gap-2 text-sm">
            <IconCheck className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
            <span>
              {label}
              {detail && <span className="text-muted-foreground"> — {detail}</span>}
            </span>
          </li>
        )
      })}
    </ul>
  )
}

function PlanPreviewCard({
  plan, projectId, features, currencies, cycle,
}: {
  plan: Plan
  projectId: string
  features: Feature[] | null | undefined
  currencies: Currency[] | null | undefined
  cycle: Cycle
}) {
  let prices: Record<string, { monthly?: number; yearly?: number }> = {}
  try {
    prices = JSON.parse(plan.prices)
  } catch {
    // leave empty — malformed prices JSON shows as "No price set" below
  }
  const codes = Object.keys(prices)
  // A pricing page shows one currency prominently, not every currency the
  // plan happens to be priced in — prefer the first active reference currency
  // that this plan actually has a price for.
  const primaryCode =
    currencies?.find((c) => c.active && codes.includes(c.code))?.code ?? codes[0]
  const otherCodes = codes.filter((c) => c !== primaryCode)
  // Every currency priced at 0 for this cycle means "no real price" — an
  // enterprise/custom tier operators leave zeroed out on purpose, not a
  // literal $0 plan. Showing "Rp0 / month" on a pricing page reads as a
  // bug, so treat it as a talk-to-sales tier instead.
  const isCustomPriced = codes.length > 0 && codes.every((c) => (prices[c]?.[cycle] ?? 0) === 0)

  return (
    <div className="flex flex-col gap-5 rounded-xl border bg-card p-6">
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-heading text-lg font-semibold">{plan.name}</h3>
        {!plan.active && <Badge variant="secondary" className="text-xs">inactive</Badge>}
      </div>
      {plan.description && <p className="-mt-3 text-sm text-muted-foreground">{plan.description}</p>}

      {/* The subtext line always renders (blank if there's nothing to say)
          so every card reserves the same price-block height — otherwise a
          custom-priced or single-currency plan's block is one line shorter
          and its CTA button below sits higher than the other cards'. */}
      <div>
        {isCustomPriced ? (
          <span className="text-3xl font-bold">Custom pricing</span>
        ) : primaryCode ? (
          <>
            <span className="text-3xl font-bold tabular-nums">
              {formatMoney(prices[primaryCode]?.[cycle], primaryCode, currencies)}
            </span>
            <span className="text-sm text-muted-foreground"> / {cycle === "monthly" ? "month" : "year"}</span>
          </>
        ) : (
          <span className="text-3xl font-bold text-muted-foreground">No price set</span>
        )}
        <p className="mt-1 text-xs text-muted-foreground">
          {isCustomPriced
            ? "Contact us for a quote"
            : otherCodes.length > 0
              ? `Also billed in ${otherCodes.join(", ")}`
              : " "}
        </p>
      </div>

      <Button className="w-full" disabled>
        {isCustomPriced ? "Contact sales" : `Choose ${plan.name}`}
      </Button>

      <div className="border-t pt-4">
        <PlanPreviewEntitlements projectId={projectId} planId={plan.id} features={features} />
      </div>
    </div>
  )
}

interface PlanPreviewDialogProps {
  projectId: string
  plans: Plan[]
  features: Feature[] | null | undefined
  onClose: () => void
}

export function PlanPreviewDialog({ projectId, plans, features, onClose }: PlanPreviewDialogProps) {
  const { data: currencies } = useCurrencies(projectId)
  const [cycle, setCycle] = useState<Cycle>("monthly")
  const sorted = [...plans].sort((a, b) => a.sort_order - b.sort_order)

  return (
    <AlertDialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <AlertDialogContent className="max-h-[85vh] w-[95vw] !max-w-6xl overflow-y-auto">
        <AlertDialogHeader>
          <AlertDialogTitle>Plan preview</AlertDialogTitle>
          <AlertDialogDescription>
            How the pricing page would present today's plans, prices, and entitlements.
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div className="mx-auto flex w-fit items-center gap-1 rounded-lg bg-muted p-1">
          <Button size="sm" variant={cycle === "monthly" ? "default" : "ghost"} onClick={() => setCycle("monthly")}>
            Monthly
          </Button>
          <Button size="sm" variant={cycle === "yearly" ? "default" : "ghost"} onClick={() => setCycle("yearly")}>
            Yearly
          </Button>
        </div>

        {sorted.length === 0 ? (
          <p className="py-8 text-center text-sm text-muted-foreground">No plans found</p>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {sorted.map((plan) => (
              <PlanPreviewCard
                key={plan.id}
                plan={plan}
                projectId={projectId}
                features={features}
                currencies={currencies}
                cycle={cycle}
              />
            ))}
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel onClick={onClose}>Close</AlertDialogCancel>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
