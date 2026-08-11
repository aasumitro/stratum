import { useState } from "react"
import { IconCheck } from "@tabler/icons-react"
import type { UserResult } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useSearchUsers } from "@/hooks/use-support"
import { Input } from "@/components/ui/input"

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
        <div className="max-h-48 divide-y overflow-y-auto rounded-md border bg-popover shadow-md">
          {results!.map((u) => (
            <button
              key={u.auth_sub}
              type="button"
              className="w-full px-3 py-2 text-left text-sm transition-colors hover:bg-accent"
              onClick={() => {
                onSelect(u)
                setQ("")
              }}
            >
              <div className="font-medium">{u.full_name}</div>
              <div className="text-xs text-muted-foreground">{u.email}</div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
