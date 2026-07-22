// Heuristic: DLQ queues typically contain "dlq", "dead", or "dlx" in their name.
export function isDLQ(name: string): boolean {
  const n = name.toLowerCase()
  return n.includes("dlq") || n.includes("dead") || n.includes("dlx")
}

export function StateDot({ state }: { state: string }) {
  const colors: Record<string, string> = {
    running: "bg-green-500",
    idle: "bg-amber-500",
    down: "bg-red-500",
    stopped: "bg-red-500",
  }
  return (
    <span
      className={`inline-block size-2 rounded-full ${colors[state] ?? "bg-muted-foreground"}`}
      title={state}
    />
  )
}
