export const ACTION_STYLE: Record<string, string> = {
  extend_trial:
    "bg-blue-500/10 text-blue-700 dark:text-blue-400 border-blue-500/20",
  activate_subscription:
    "bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20",
  change_plan:
    "bg-purple-500/10 text-purple-700 dark:text-purple-400 border-purple-500/20",
  mark_invoice_paid:
    "bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20",
  void_invoice:
    "bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20",
  suspend_organization:
    "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20",
  unsuspend_organization:
    "bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20",
}

export function actionLabel(action: string): string {
  return action.replace(/_/g, " ")
}

export function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60_000)
  if (mins < 1) return "just now"
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}
