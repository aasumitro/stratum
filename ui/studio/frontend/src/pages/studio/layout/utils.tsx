export function monitorStatusDot(status: string | undefined) {
  if (!status) return null
  const colors: Record<string, string> = {
    ok: "bg-green-500",
    degraded: "bg-amber-500",
    down: "bg-red-500",
  }
  return (
    <span
      className={`size-2 shrink-0 rounded-full ${colors[status] ?? "bg-muted-foreground/40"}`}
    />
  )
}
