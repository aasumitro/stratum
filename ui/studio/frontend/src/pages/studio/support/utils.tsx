import { Badge } from "@/components/ui/badge"
import { formatDate } from "../components/table-helpers"

export { formatDate }

export function formatCents(cents: number, currency: string) {
  return new Intl.NumberFormat("en-US", { style: "currency", currency }).format(cents / 100)
}

export function formatDateTime(iso: string | null | undefined) {
  if (!iso) return "—"
  return new Date(iso).toLocaleString("en-US", {
    year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit",
  })
}

export function lastSeenDot(ts: string | null | undefined) {
  if (!ts) return <span className="size-2 rounded-full bg-muted-foreground/30 flex-shrink-0" />
  const diffDays = (Date.now() - new Date(ts).getTime()) / 86_400_000
  const color = diffDays < 1 ? "bg-green-500" : diffDays < 7 ? "bg-amber-500" : "bg-muted-foreground/30"
  return <span className={`size-2 rounded-full flex-shrink-0 ${color}`} />
}

export function billingBadge(status: string) {
  const map: Record<string, string> = {
    active:    "default",
    trialing:  "secondary",
    cancelled: "outline",
    past_due:  "destructive",
    expired:   "destructive",
  }
  return (
    <Badge variant={(map[status] ?? "outline") as never} className="text-xs">
      {status || "—"}
    </Badge>
  )
}

export function roleBadge(role: string) {
  const map: Record<string, string> = {
    owner: "default",
    admin: "secondary",
    member: "outline",
  }
  return (
    <Badge variant={(map[role] ?? "outline") as never} className="text-xs">
      {role}
    </Badge>
  )
}

export function initials(name: string) {
  return name
    .split(" ")
    .slice(0, 2)
    .map((w) => w[0] ?? "")
    .join("")
    .toUpperCase() || "?"
}
