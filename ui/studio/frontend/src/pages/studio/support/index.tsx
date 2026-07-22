import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import { IconHeadset, IconSearch, IconUser } from "@tabler/icons-react"
import { useSearchUsers } from "@/hooks/use-support"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/ui"
import { initials, lastSeenDot } from "./utils"
import { AtRiskTab } from "./at-risk-tab"
import { UserDetailPanel } from "./user-detail-panel"

type SupportTab = "users" | "at-risk"

export function SupportPage() {
  const { projectId } = useParams({ from: "/studio/$projectId" })
  const [tab, setTab] = useState<SupportTab>("users")
  const [query, setQuery] = useState("")
  const [selectedAuthSub, setSelectedAuthSub] = useState<string | null>(null)

  const { data: results, isLoading: searching } = useSearchUsers(projectId, query)

  const TAB_BTN = (active: boolean) =>
    cn(
      "px-4 py-2 text-sm font-medium border-b-2 transition-colors",
      active
        ? "border-foreground text-foreground"
        : "border-transparent text-muted-foreground hover:text-foreground",
    )

  return (
    <div className="flex flex-col min-h-full">
      {/* Header */}
      <header className="flex items-center gap-3 px-6 py-4 border-b">
        <IconHeadset className="size-4 text-muted-foreground" />
        <div className="flex flex-col gap-0.5">
          <h1 className="font-semibold text-sm">Support</h1>
          <p className="text-xs text-muted-foreground">
            Look up users, manage subscriptions, resolve invoices
          </p>
        </div>
      </header>

      {/* Tab bar */}
      <div className="flex items-center px-6 border-b bg-muted/20">
        <button type="button" className={TAB_BTN(tab === "users")} onClick={() => setTab("users")}>
          Users
        </button>
        <button type="button" className={TAB_BTN(tab === "at-risk")} onClick={() => setTab("at-risk")}>
          At-risk Invoices
        </button>
      </div>

      {tab === "at-risk" ? (
        <AtRiskTab projectId={projectId} />
      ) : (
        <div className="flex flex-1 min-h-0">
          {/* Left: search panel */}
          <div className="w-72 flex-shrink-0 border-r flex flex-col">
            <div className="p-3 border-b">
              <div className="relative">
                <IconSearch className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground" />
                <Input
                  placeholder="Filter by email or name…"
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value)
                    setSelectedAuthSub(null)
                  }}
                  className="pl-8 h-8 text-sm"
                />
              </div>
            </div>

            <div className="flex-1 overflow-y-auto">
              {searching ? (
                <div className="animate-pulse p-3 flex flex-col gap-2">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <div key={i} className="h-14 bg-muted rounded-lg" />
                  ))}
                </div>
              ) : !results || results.length === 0 ? (
                <p className="text-xs text-muted-foreground text-center py-8 px-4">No users found</p>
              ) : (
                results.map((u) => (
                  <button
                    key={u.auth_sub}
                    onClick={() => setSelectedAuthSub(u.auth_sub)}
                    className={cn(
                      "w-full flex items-center gap-3 px-3 py-3 text-left border-b last:border-b-0 transition-colors",
                      selectedAuthSub === u.auth_sub
                        ? "bg-accent"
                        : "hover:bg-muted/50",
                    )}
                  >
                    <div className="size-8 rounded-full bg-muted flex items-center justify-center text-xs font-semibold flex-shrink-0">
                      {initials(u.full_name)}
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium truncate">{u.full_name || "—"}</p>
                      <p className="text-xs text-muted-foreground truncate">{u.email}</p>
                    </div>
                    {lastSeenDot(u.last_seen_at)}
                  </button>
                ))
              )}
            </div>
          </div>

          {/* Right: detail panel */}
          <div className="flex-1 overflow-y-auto p-6">
            {!selectedAuthSub ? (
              <div className="flex flex-col items-center justify-center h-full gap-3 text-center">
                <div className="size-12 rounded-full bg-muted flex items-center justify-center">
                  <IconUser className="size-6 text-muted-foreground" />
                </div>
                <p className="text-sm text-muted-foreground">Search for a user on the left</p>
              </div>
            ) : (
              <UserDetailPanel projectId={projectId} authSub={selectedAuthSub} />
            )}
          </div>
        </div>
      )}
    </div>
  )
}
