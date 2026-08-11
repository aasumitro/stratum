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

  const { data: results, isLoading: searching } = useSearchUsers(
    projectId,
    query
  )

  const TAB_BTN = (active: boolean) =>
    cn(
      "border-b-2 px-4 py-2 text-sm font-medium transition-colors",
      active
        ? "border-foreground text-foreground"
        : "border-transparent text-muted-foreground hover:text-foreground"
    )

  return (
    <div className="flex min-h-full flex-col">
      {/* Header */}
      <header className="flex items-center gap-3 border-b px-6 py-4">
        <IconHeadset className="size-4 text-muted-foreground" />
        <div className="flex flex-col gap-0.5">
          <h1 className="text-sm font-semibold">Support</h1>
          <p className="text-xs text-muted-foreground">
            Look up users, manage subscriptions, resolve invoices
          </p>
        </div>
      </header>

      {/* Tab bar */}
      <div className="flex items-center border-b bg-muted/20 px-6">
        <button
          type="button"
          className={TAB_BTN(tab === "users")}
          onClick={() => setTab("users")}
        >
          Users
        </button>
        <button
          type="button"
          className={TAB_BTN(tab === "at-risk")}
          onClick={() => setTab("at-risk")}
        >
          At-risk Invoices
        </button>
      </div>

      {tab === "at-risk" ? (
        <AtRiskTab projectId={projectId} />
      ) : (
        <div className="flex min-h-0 flex-1">
          {/* Left: search panel */}
          <div className="flex w-72 shrink-0 flex-col border-r">
            <div className="border-b p-3">
              <div className="relative">
                <IconSearch className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  placeholder="Filter by email or name…"
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value)
                    setSelectedAuthSub(null)
                  }}
                  className="h-8 pl-8 text-sm"
                />
              </div>
            </div>

            <div className="flex-1 overflow-y-auto">
              {searching ? (
                <div className="flex animate-pulse flex-col gap-2 p-3">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <div key={i} className="h-14 rounded-lg bg-muted" />
                  ))}
                </div>
              ) : !results || results.length === 0 ? (
                <p className="px-4 py-8 text-center text-xs text-muted-foreground">
                  No users found
                </p>
              ) : (
                results.map((u) => (
                  <button
                    key={u.auth_sub}
                    onClick={() => setSelectedAuthSub(u.auth_sub)}
                    className={cn(
                      "flex w-full items-center gap-3 border-b px-3 py-3 text-left transition-colors last:border-b-0",
                      selectedAuthSub === u.auth_sub
                        ? "bg-accent"
                        : "hover:bg-muted/50"
                    )}
                  >
                    <div className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold">
                      {initials(u.full_name)}
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">
                        {u.full_name || "—"}
                      </p>
                      <p className="truncate text-xs text-muted-foreground">
                        {u.email}
                      </p>
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
              <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
                <div className="flex size-12 items-center justify-center rounded-full bg-muted">
                  <IconUser className="size-6 text-muted-foreground" />
                </div>
                <p className="text-sm text-muted-foreground">
                  Search for a user on the left
                </p>
              </div>
            ) : (
              <UserDetailPanel
                projectId={projectId}
                authSub={selectedAuthSub}
              />
            )}
          </div>
        </div>
      )}
    </div>
  )
}
