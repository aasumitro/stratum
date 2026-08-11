import { useState } from "react"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { cn } from "@/lib/ui"

function GuideSection({
  intro,
  children,
}: {
  intro: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-muted-foreground">{intro}</p>
      <div className="flex flex-col gap-3 text-muted-foreground">
        {children}
      </div>
    </div>
  )
}

function GuidePoint({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <p className="text-xs font-medium tracking-wide text-foreground uppercase">
        {label}
      </p>
      <p>{children}</p>
    </div>
  )
}

type GuideTab = "features" | "plans" | "addons" | "coupons"

const GUIDE_TABS: { id: GuideTab; label: string }[] = [
  { id: "features", label: "1. Features" },
  { id: "plans", label: "2. Plans" },
  { id: "addons", label: "3. Addons" },
  { id: "coupons", label: "4. Coupons" },
]

export function CatalogGuideSheet({ onClose }: { onClose: () => void }) {
  const [guideTab, setGuideTab] = useState<GuideTab>("features")

  return (
    <Sheet
      open
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <SheetContent className="flex w-full flex-col gap-0 overflow-y-auto sm:max-w-2xl">
        <SheetHeader className="pb-4">
          <SheetTitle>
            How Plans, Features, Coupons &amp; Addons fit together
          </SheetTitle>
          <SheetDescription>
            Build in this order — each tab only needs the ones before it. Skip
            Addons/Coupons entirely if you just need flat subscription tiers.
          </SheetDescription>
        </SheetHeader>

        <div className="flex border-b px-6">
          {GUIDE_TABS.map((t) => (
            <button
              key={t.id}
              onClick={() => setGuideTab(t.id)}
              className={cn(
                "border-b-2 px-4 py-2.5 text-sm transition-colors",
                guideTab === t.id
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              )}
            >
              {t.label}
            </button>
          ))}
        </div>

        <div className="mx-6 flex flex-col gap-4 overflow-y-auto py-4 text-sm">
          {guideTab === "features" && (
            <GuideSection
              intro={
                <>
                  The atomic capabilities a customer can be granted — e.g.
                  "Members", "Storage", "Priority Support". Created once, then
                  reused across any number of Plans and Addons.
                </>
              }
            >
              <GuidePoint label="The 4 types">
                <strong>Metered</strong> — a number, checked against real
                tracked usage (e.g. members, storage). This is the only type
                with live enforcement: the API blocks an action once actual
                usage meets the limit. <strong>Boolean</strong> — simple on/off
                access, no value; just being attached to a plan/addon turns it
                on. <strong>Static</strong> — a fixed, informational label (e.g.
                "SLA Guarantee") with no value at all — purely for display.{" "}
                <strong>Config</strong> — delivers an arbitrary JSON value to
                the app (e.g. a rate limit tier, a retention window) — this is
                the right choice for any "setting" the app reads and applies
                itself, rather than a quota checked against live usage.
              </GuidePoint>
              <GuidePoint label="Metric key (metered only)">
                Must match the metric name the backend actually records usage
                under (e.g. <code className="font-mono">storage_bytes</code>).
                Get this wrong and the limit is seeded but never enforced, since
                nothing increments a metric that doesn't exist. Naming it with a{" "}
                <code className="font-mono">_bytes</code> suffix also makes
                Studio auto-format the value as KB/MB/GB instead of a raw byte
                count — matching, not coincidental.
              </GuidePoint>
              <GuidePoint label="Description">
                Shown in the Features list and in the entitlement picker when
                attaching features to a Plan/Addon — the more similarly-named
                features you have, the more this matters for telling them apart
                later.
              </GuidePoint>
            </GuideSection>
          )}

          {guideTab === "plans" && (
            <GuideSection
              intro={
                <>
                  The base subscription tiers customers pay for and choose
                  between. A $0 plan (like the seeded "Custom") is the signal a
                  pricing page uses to show "Contact Us" instead of "Subscribe."
                </>
              }
            >
              <GuidePoint label="Prices (JSONB)">
                Keyed by currency code, each with{" "}
                <code className="font-mono">monthly</code>/
                <code className="font-mono">yearly</code> amounts — but as{" "}
                <strong>integers in that currency's smallest unit</strong>, not
                the display price. Multiply the real price by{" "}
                <code className="font-mono">10^decimal_places</code> for that
                currency: USD has 2 decimal places (cents), so $9.00 →{" "}
                <code className="font-mono">900</code>. IDR has 0 decimal
                places, so Rp 13.500 → <code className="font-mono">13500</code>{" "}
                exactly, no ×100. Use the <strong>Generate stub</strong> button
                to get the correct currency keys pre-filled at zero, and the{" "}
                <strong>Confirm</strong> step before saving decodes every value
                back to a real price so a mistake (like forgetting to convert
                one currency but not another) is visible before it's written.
              </GuidePoint>
              <GuidePoint label="Entitlements (Manage Entitlements)">
                What this plan actually grants, per feature. For metered
                features, the value is an <strong>absolute cap</strong> (Solo →
                1 member, Growth → 15 members) — use{" "}
                <code className="font-mono">-1</code> for unlimited. For
                boolean/static features, there's no value — a feature's mere
                presence in the list is the entitlement. For config features,
                the value is the JSON payload delivered to the app.
              </GuidePoint>
              <GuidePoint label="Sort order / Active">
                Sort order controls display order on a pricing page (lower shown
                first). Inactive plans are hidden from new signups but existing
                subscribers on that plan are unaffected.
              </GuidePoint>
            </GuideSection>
          )}

          {guideTab === "addons" && (
            <GuideSection
              intro={
                <>
                  Extras a subscriber buys <strong>on top of</strong> their plan
                  — e.g. "+5 Members" for $5/mo. Same Prices JSONB shape as
                  Plans (same currency/decimal-place math applies), and addons
                  bill{" "}
                  <strong>
                    recurring, alongside the subscription's own cycle
                  </strong>
                  , for as long as they stay attached — not a one-time purchase.
                </>
              }
            >
              <GuidePoint label="Entitlements are ADDITIVE, not absolute — the one thing to get right">
                An addon's entitlement value stacks <strong>on top of</strong>{" "}
                whatever the base plan already grants — it is never a
                replacement value. A Solo subscriber (1 member) with a "+1
                Member" addon attached effectively has 2, not 1. This is the
                opposite convention from a Plan's entitlement, which is why
                Studio's entitlement table prefixes addon values with{" "}
                <code className="font-mono">+</code>.
              </GuidePoint>
            </GuideSection>
          )}

          {guideTab === "coupons" && (
            <GuideSection
              intro={
                <>
                  Discount codes, redeemed against a subscription to reduce what
                  it's billed.
                </>
              }
            >
              <GuidePoint label="Discount type">
                <strong>Fixed</strong> — a flat amount off, in a specific
                currency, encoded the same smallest-unit way as Plan/Addon
                prices. <strong>Percent</strong> — a percentage off the invoice
                subtotal, 0–100.
              </GuidePoint>
              <GuidePoint label="Cadence">
                How many invoices the discount applies to{" "}
                <strong>after one redemption</strong> — this is not related to
                how many times the code can be used. <strong>Once</strong> =
                next invoice only, then full price. <strong>Repeated</strong> =
                a fixed number of cycles (set via Number of billing cycles),
                then full price. <strong>Forever</strong> = discounted for the
                life of the subscription.
              </GuidePoint>
              <GuidePoint label="Redeem window &amp; Max redemptions">
                Redeem After/Before optionally bound when the code is valid. Max
                redemptions caps how many times the code can be redeemed in
                total, <strong>across every customer combined</strong> — not per
                organization or per user.
              </GuidePoint>
              <GuidePoint label="Targets (Manage Targets)">
                Restricts <strong>who</strong> can redeem. No targets =
                redeemable by anyone. Add specific organizations/users to
                restrict the code to an allow-list.
              </GuidePoint>
            </GuideSection>
          )}
        </div>

        <SheetFooter className="mt-auto pt-4">
          <Button onClick={onClose}>Got it</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
