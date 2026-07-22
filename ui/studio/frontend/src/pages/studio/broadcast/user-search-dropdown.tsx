import { useState } from "react"
import { IconCheck } from "@tabler/icons-react"
import type { OrganizationSummary, UserResult } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useSearchUsers } from "@/hooks/use-support"
import { Input } from "@/components/ui/input"

export type TargetType = "all" | "organization" | "user"

export function buildTarget(type: TargetType, organization: OrganizationSummary | null, user: UserResult | null): string {
  if (type === "all") return "all"
  if (type === "organization") return organization ? `organization:${organization.id}` : ""
  return user ? `user:${user.auth_sub}` : ""
}

export function formatTarget(target: string, organization: OrganizationSummary | null, user: UserResult | null): string {
  if (target === "all") return "All users"
  if (target.startsWith("organization:")) return organization ? `${organization.name} (${organization.slug})` : `Organization: ${target.slice(13).slice(0, 8)}…`
  if (target.startsWith("user:")) return user ? `${user.full_name} — ${user.email}` : `User: ${target.slice(5).slice(0, 12)}…`
  return target
}

export function UserSearchDropdown({
  projectId,
  selected,
  onSelect,
}: {
  projectId: string
  selected: UserResult | null
  onSelect: (u: UserResult) => void
}) {
  const [q, setQ] = useState("")
  const { data: results } = useSearchUsers(projectId, q)
  const showResults = !selected && results && results.length > 0

  return (
    <div className="space-y-2">
      <Input
        placeholder="Search by email or name…"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      {selected && (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <IconCheck className="size-3.5 text-green-500" />
          {selected.full_name} — {selected.email}
        </div>
      )}
      {showResults && (
        <div className="border rounded-md divide-y max-h-48 overflow-y-auto bg-popover shadow-md">
          {results!.map((u) => (
            <button
              key={u.auth_sub}
              type="button"
              className="w-full text-left px-3 py-2 text-sm hover:bg-accent transition-colors"
              onClick={() => {
                onSelect(u)
                setQ("")
              }}
            >
              <div className="font-medium">{u.full_name}</div>
              <div className="text-muted-foreground text-xs">{u.email}</div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
