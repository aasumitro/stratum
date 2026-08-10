import type { AttachedAddon, BillingCycle } from "@/types/billing"

// AddonChangeMetadata mirrors the backend's addonChangeMetadata
// (service_addon.go) — the shape of subscription_history.metadata for every
// "addon_change" row. pending marks the one row that doesn't mean the
// quantity already changed: requestAddonIncrease's invoice-creation row
// records a quantity increase that's only requested, gated on that
// invoice's payment.
export interface AddonChangeMetadata {
  addon_id: string
  from_quantity: number
  to_quantity: number
  pending?: boolean
}

export function parseAddonChangeMetadata(
  metadata: unknown
): AddonChangeMetadata | null {
  if (typeof metadata !== "object" || metadata === null) return null
  const m = metadata as Record<string, unknown>
  if (typeof m.addon_id !== "string") return null
  if (typeof m.from_quantity !== "number" || typeof m.to_quantity !== "number")
    return null
  return {
    addon_id: m.addon_id,
    from_quantity: m.from_quantity,
    to_quantity: m.to_quantity,
    pending: m.pending === true,
  }
}

export function formatPrice(
  amount: number,
  currency: string,
  cycle: BillingCycle,
  t: (k: string) => string
): string {
  const suffix =
    cycle === "monthly"
      ? t("billing.plans.perMonth")
      : t("billing.plans.perYear")
  if (currency === "IDR") return `Rp${amount.toLocaleString("id-ID")}${suffix}`
  return `$${(amount / 100).toLocaleString("en-US")}${suffix}`
}

export interface AmendmentChange {
  addonId: string
  fromQty: number
  toQty: number
}

export interface AmendmentDiff {
  immediate: AmendmentChange[]
  scheduled: AmendmentChange[]
}

// Diffs the addon picker's staged target quantities against each addon's
// current live quantity. `staged` only ever carries entries with qty > 0
// (AddAddonsDialog's own "Add Selected" commit filters zeroes out), so an
// addon dropped from it entirely means the same thing as an explicit 0 —
// both read as a target of 0 here. toQty > fromQty (including a brand-new
// attach, fromQty = 0) buckets as "immediate"; toQty < fromQty (a decrease
// or full removal) buckets as "scheduled" — matching the backend's own
// attachAddon/detachAddon branch on this same live-vs-requested comparison,
// so this diff can never disagree with what the backend actually does with
// the resulting attach/detach calls.
export function computeAmendmentDiff(
  staged: Record<string, number>,
  currentlyAttached: AttachedAddon[]
): AmendmentDiff {
  const fromQtyById = new Map(
    currentlyAttached.map((a) => [a.addon_id, a.quantity])
  )
  const addonIds = new Set([...fromQtyById.keys(), ...Object.keys(staged)])

  const immediate: AmendmentChange[] = []
  const scheduled: AmendmentChange[] = []
  for (const addonId of addonIds) {
    const fromQty = fromQtyById.get(addonId) ?? 0
    const toQty = staged[addonId] ?? 0
    if (toQty === fromQty) continue
    const change = { addonId, fromQty, toQty }
    if (toQty > fromQty) immediate.push(change)
    else scheduled.push(change)
  }
  return { immediate, scheduled }
}

// Fires the mutations a confirmed diff requires and waits for every one of
// them to settle before returning — both buckets call the same
// attach/detach hooks either way, it's the backend, not this UI, that
// decides whether a given call lands immediately or gets scheduled, based
// on the subscription's own status. Returns the subset of changes whose
// mutation rejected, so a caller with a confirmation dialog can keep it open
// and let the user retry instead of closing on a partial failure — retrying
// re-fires the full diff, which is safe since attach/detach are idempotent
// on the backend.
export async function applyAmendmentDiff(
  diff: AmendmentDiff,
  attach: (variables: {
    addon_id: string
    quantity: number
  }) => Promise<unknown>,
  detach: (variables: string) => Promise<unknown>
): Promise<AmendmentChange[]> {
  const changes = [...diff.immediate, ...diff.scheduled]
  const results = await Promise.allSettled(
    changes.map((c) =>
      c.toQty === 0
        ? detach(c.addonId)
        : attach({ addon_id: c.addonId, quantity: c.toQty })
    )
  )
  return changes.filter((_, i) => results[i].status === "rejected")
}
