export function formatMoney(amount: number): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 0,
  }).format(amount)
}

export const STATUS_COLORS: Record<string, string> = {
  active: "bg-green-500/15 text-green-700 dark:text-green-400",
  trialing: "bg-blue-500/15 text-blue-700 dark:text-blue-400",
  cancelled: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  expired: "bg-red-500/15 text-red-700 dark:text-red-400",
  past_due: "bg-orange-500/15 text-orange-700 dark:text-orange-400",
}
