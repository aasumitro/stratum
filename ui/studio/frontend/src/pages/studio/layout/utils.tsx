export function monitorStatusDot(status: string | undefined) {
  if (!status) return null
  const colors: Record<string, string> = {
    ok:       "bg-green-500",
    degraded: "bg-amber-500",
    down:     "bg-red-500",
  }
  return (
    <span
      className={`size-2 rounded-full flex-shrink-0 ${colors[status] ?? "bg-muted-foreground/40"}`}
    />
  )
}
