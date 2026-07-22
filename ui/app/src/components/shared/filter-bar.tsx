import type { ReactNode } from "react"
import { IconX } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/ui"

export interface FilterChip {
  key: string
  label: ReactNode
  onRemove: () => void
}

interface FilterBarProps {
  chips: FilterChip[]
  onClearAll?: () => void
  clearAllLabel?: string
  /** filter controls (selects, date range, etc.) rendered before the chips */
  children?: ReactNode
  className?: string
}

/**
 * Presentational active-filter chip row. Filter *state* stays with the
 * page, wired to its route's search params via TanStack Router's
 * `useSearch`/`navigate({ search })` — that's route-specific typed plumbing
 * this generic component shouldn't own, so URL sync is the caller's job.
 */
export function FilterBar({
  chips,
  onClearAll,
  clearAllLabel = "Clear filters",
  children,
  className,
}: FilterBarProps) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>
      {children}
      {chips.map((chip) => (
        <Button
          key={chip.key}
          variant="secondary"
          size="sm"
          className="h-7 gap-1 rounded-full pr-2 text-xs"
          onClick={chip.onRemove}
        >
          {chip.label}
          <IconX className="size-3" />
        </Button>
      ))}
      {chips.length > 0 && onClearAll && (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 text-xs text-muted-foreground"
          onClick={onClearAll}
        >
          {clearAllLabel}
        </Button>
      )}
    </div>
  )
}
