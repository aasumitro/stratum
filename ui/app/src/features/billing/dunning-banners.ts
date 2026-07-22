import type { BannerItem, BannerSeverity } from "@/components/shared/banner"
import type { Invoice, Subscription } from "@/types/billing"

// In-app twin of the backend's day-3/day-7 dunning email schedule.
// The exact T-7d/T-3d/T+3d/T+7d cadence is only observable server-side (it's
// driven by RabbitMQ delayed messages, not a queryable field), so this is a
// best-effort derivation from what the frontend CAN see: subscription
// status, trial_end, and the oldest pending invoice's due_at — close enough
// to give the right banner at the right severity, not a literal mirror of
// the backend's exact timing.
export function computeDunningBanners(
  sub: Subscription | undefined,
  invoices: Invoice[],
  t: (key: string, opts?: Record<string, unknown>) => string
): BannerItem[] {
  if (!sub) return []
  const banners: BannerItem[] = []
  const now = Date.now()

  if (sub.status === "expired") {
    banners.push({
      id: "dunning-expired",
      severity: "critical",
      message: t("billing.dunning.expired"),
    })
  } else if (sub.status === "past_due") {
    const pending = invoices
      .filter((i) => i.status === "pending" && i.due_at)
      .sort(
        (a, b) => new Date(a.due_at!).getTime() - new Date(b.due_at!).getTime()
      )[0]
    const daysOverdue = pending?.due_at
      ? Math.floor((now - new Date(pending.due_at).getTime()) / 86400000)
      : 0
    let severity: BannerSeverity = "warning"
    let key = "billing.dunning.pastDue"
    if (daysOverdue >= 7) {
      severity = "critical"
      key = "billing.dunning.finalNotice"
    } else if (daysOverdue >= 3) {
      key = "billing.dunning.overdue"
    }
    banners.push({
      id: "dunning-past-due",
      severity,
      message: t(key, { days: daysOverdue }),
    })
  } else if (sub.status === "trialing" && sub.trial_end) {
    const daysLeft = Math.ceil(
      (new Date(sub.trial_end).getTime() - now) / 86400000
    )
    if (daysLeft <= 2 && daysLeft >= 0) {
      banners.push({
        id: "dunning-trial-ending",
        severity: "info",
        message: t("billing.dunning.trialEndingSoon", { days: daysLeft }),
      })
    }
  } else if (sub.status === "cancelled" && sub.period_end) {
    banners.push({
      id: "dunning-cancelled",
      severity: "info",
      message: t("billing.dunning.cancelledAccessUntil", {
        date: new Date(sub.period_end).toLocaleDateString(undefined, {
          year: "numeric",
          month: "short",
          day: "numeric",
        }),
      }),
    })
  }

  return banners
}
