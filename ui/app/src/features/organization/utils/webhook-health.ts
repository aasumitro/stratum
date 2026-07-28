export function healthTone(percent: number, textOnly = false): string {
  if (percent >= 90)
    return textOnly
      ? "text-emerald-600 dark:text-emerald-400"
      : "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
  if (percent >= 70)
    return textOnly
      ? "text-amber-600 dark:text-amber-400"
      : "bg-amber-500/10 text-amber-600 dark:text-amber-400"
  return textOnly ? "text-destructive" : "bg-destructive/10 text-destructive"
}
