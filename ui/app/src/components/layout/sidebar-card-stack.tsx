import type { ReactNode } from "react"

interface SidebarCardStackProps {
  /** Ordered by priority — only the first non-null card renders. */
  cards: ReactNode[]
}

/**
 * Slot in the sidebar footer for one contextual card at a time (setup
 * checklist today; announcements/changelog later share the same slot).
 * Callers pass cards in priority order — the first truthy one wins.
 */
export function SidebarCardStack({ cards }: SidebarCardStackProps) {
  const card = cards.find(Boolean)
  if (!card) return null

  return (
    <div className="px-2 pb-2 group-data-[collapsible=icon]:hidden">{card}</div>
  )
}
