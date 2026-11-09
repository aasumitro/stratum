import type { Subscription, SubscriptionStatus } from "@/types/billing"

type ScheduleFields = Pick<
  Subscription,
  "period_end" | "scheduled_effective_at"
>

// The date a scheduled downgrade or add-on decrease takes effect. The API
// keeps it apart from period_end, which moves on once the renewal is paid.
export function scheduledChangeDate(sub: ScheduleFields): string | undefined {
  return sub.scheduled_effective_at ?? sub.period_end
}

// Once the renewal priced from the schedule is paid (period_end moved past
// the effective date), the schedule can no longer be undone or changed.
export function scheduledChangeLocked(sub: ScheduleFields): boolean {
  if (!sub.scheduled_effective_at || !sub.period_end) return false
  return (
    new Date(sub.period_end).getTime() >
    new Date(sub.scheduled_effective_at).getTime()
  )
}

// A downgrade waits for the end of a paid period. A trial has none to protect,
// and an ended subscription none running, so there it applies at once.
export function downgradeAppliesNow(status: SubscriptionStatus): boolean {
  return status === "trialing" || status === "expired" || status === "cancelled"
}
