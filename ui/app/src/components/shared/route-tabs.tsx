import type { ReactNode } from "react"
import { Link, useRouterState } from "@tanstack/react-router"
import { cn } from "@/lib/ui"

export interface RouteTab {
  label: string
  suffix: string
  badge?: ReactNode
}

interface RouteTabsProps {
  base: string
  tabs: readonly RouteTab[]
}

// Shared route-backed tab bar for the org module's sub-nav pages (Settings,
// Billing, Members). Renders Link, not @/components/ui/tabs.tsx's Tabs —
// that primitive is client-state controlled (Base UI value/onValueChange),
// which would make every tab lose deep-linking/back-button support.
// overflow-x-auto replaces the per-page flex-wrap so a long tab row scrolls
// instead of wrapping to a second line on narrow viewports.
export function RouteTabs({ base, tabs }: RouteTabsProps) {
  const { location } = useRouterState()

  return (
    <nav className="flex gap-1 overflow-x-auto whitespace-nowrap">
      {tabs.map((tab) => {
        const href = `${base}${tab.suffix}`
        const isActive =
          tab.suffix === ""
            ? location.pathname === base || location.pathname === `${base}/`
            : location.pathname === href
        return (
          <Link
            key={tab.suffix || "root"}
            to={href as string}
            className={cn(
              "flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition-colors",
              isActive
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-accent hover:text-foreground"
            )}
          >
            {tab.label}
            {tab.badge}
          </Link>
        )
      })}
    </nav>
  )
}
