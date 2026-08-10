import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"

interface SettingsSectionPreviewListProps<T> {
  items: T[]
  max?: number
  keyExtractor: (item: T) => string
  renderItem: (item: T) => ReactNode
  moreLabel: (remaining: number) => string
  onViewAll: () => void
}

/**
 * Caps a list to its first N items, rendered as one bordered/divided list
 * (matching `MembersTable`'s own bordered-table look, not stacked boxed
 * cards), with a "+N more" footer that opens the full view. Row rendering
 * is fully delegated via `renderItem`; this owns only the shared container,
 * the cap, and the remainder count.
 */
export function SettingsSectionPreviewList<T>({
  items,
  max = 5,
  keyExtractor,
  renderItem,
  moreLabel,
  onViewAll,
}: SettingsSectionPreviewListProps<T>) {
  const shown = items.slice(0, max)
  const remaining = items.length - shown.length

  return (
    <div className="flex flex-col">
      <div className="divide-y overflow-hidden rounded-xl border bg-card shadow-sm">
        {shown.map((item) => (
          <div key={keyExtractor(item)}>{renderItem(item)}</div>
        ))}
        {remaining > 0 && (
          <div className="bg-muted/10 p-1">
            <Button
              variant="ghost"
              size="sm"
              className="w-full text-xs text-muted-foreground hover:bg-muted/50 hover:text-foreground"
              onClick={onViewAll}
            >
              {moreLabel(remaining)}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
