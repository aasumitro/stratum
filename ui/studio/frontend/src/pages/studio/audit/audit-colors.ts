export function statusColor(code: number): string {
  if (code >= 500)
    return "bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20"
  if (code >= 400)
    return "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20"
  if (code >= 200)
    return "bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20"
  return "bg-muted text-muted-foreground"
}

export function methodColor(method: string): string {
  const m: Record<string, string> = {
    GET: "bg-blue-500/10 text-blue-700 dark:text-blue-400",
    POST: "bg-green-500/10 text-green-700 dark:text-green-400",
    PATCH: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
    DELETE: "bg-red-500/10 text-red-700 dark:text-red-400",
    PUT: "bg-purple-500/10 text-purple-700 dark:text-purple-400",
  }
  return m[method] ?? "bg-muted text-muted-foreground"
}
