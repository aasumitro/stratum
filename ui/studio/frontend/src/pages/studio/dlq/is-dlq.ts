// Heuristic: DLQ queues typically contain "dlq", "dead", or "dlx" in their name.
export function isDLQ(name: string): boolean {
  const n = name.toLowerCase()
  return n.includes("dlq") || n.includes("dead") || n.includes("dlx")
}
