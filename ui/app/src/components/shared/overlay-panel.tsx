import type { ReactNode } from "react"
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"
import { cn } from "@/lib/ui"

interface OverlayPanelProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  /** Rendered inline with the title in the header row (filters, export,
   * info — whatever the caller wants next to the title instead of buried
   * in the body). Wraps to a second line on narrow viewports. */
  headerContent?: ReactNode
  bodyClassName?: string
  children: ReactNode
}

/**
 * Large immersive workspace panel — full-screen on mobile, a big centred
 * panel on tablet/desktop. Built on the shared Dialog primitive (inherits
 * its focus-trap/ESC/outside-click/close-button handling); only sizing and
 * the fixed-header/scrolling-body split are custom. Presentation only — no
 * feature-specific knowledge, no dirty-state handling: whatever's passed as
 * `children` owns its own unsaved-changes guards if it needs any.
 *
 * `overflow-hidden` on the popup itself means any content inside `children`
 * that wants a confined "slide-in from the right" pane (e.g. a detail view
 * that shouldn't escape to a viewport-edge Sheet) can just use `absolute
 * inset-y-0 right-0` — the popup is already `position: fixed` (its own
 * containing block for absolutely-positioned descendants), so it clips to
 * this panel's own bounds instead of the viewport with no extra plumbing.
 * (Don't add `relative` here — it's redundant with `fixed` for that purpose
 * and, worse, conflicts with it as the same CSS `position` utility, which
 * knocks the dialog out of its floating/centred position entirely.)
 */
export function OverlayPanel({
  open,
  onOpenChange,
  title,
  description,
  headerContent,
  bodyClassName,
  children,
}: OverlayPanelProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn(
          "flex flex-col gap-0 overflow-hidden p-0",
          // Mobile: full-screen, no margin, no rounded corners.
          "inset-0 size-full max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none",
          // Tablet (≥768px): large centred panel with a visible margin.
          "md:inset-auto md:top-1/2 md:left-1/2 md:h-[92vh] md:max-h-[92vh] md:w-[95vw] md:max-w-[95vw] md:-translate-x-1/2 md:-translate-y-1/2 md:rounded-[min(var(--radius-4xl),24px)]",
          // Desktop (≥1024px): the primary ~90vw × 90vh spec.
          "lg:h-[90vh] lg:max-h-[90vh] lg:w-[90vw] lg:max-w-[90vw]"
        )}
      >
        <div className="flex shrink-0 flex-wrap items-center gap-3 px-6 py-3 pr-14">
          <div className="flex flex-col gap-1">
            <DialogTitle className="shrink-0">{title}</DialogTitle>
            {description && (
              <DialogDescription>{description}</DialogDescription>
            )}
          </div>
          {headerContent}
        </div>
        <div
          className={cn(
            "min-h-0 flex-1 overflow-y-auto",
            bodyClassName ?? "p-6"
          )}
        >
          {children}
        </div>
      </DialogContent>
    </Dialog>
  )
}
