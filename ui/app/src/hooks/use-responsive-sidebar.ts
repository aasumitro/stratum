import { useEffect, useState } from "react"

const COLLAPSE_BREAKPOINT = 1024

// The sidebar collapses to icon-only automatically at ≤1024px
// (full width above it), independent of the separate <768px mobile
// Sheet-overlay behavior already built into shadcn's Sidebar component.
// The user can still manually re-expand via the trigger; crossing the
// breakpoint again re-applies the automatic state.
export function useResponsiveSidebarOpen() {
  const [open, setOpen] = useState(
    () =>
      typeof window === "undefined" || window.innerWidth > COLLAPSE_BREAKPOINT
  )

  useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${COLLAPSE_BREAKPOINT}px)`)
    function handleChange(e: MediaQueryListEvent | MediaQueryList) {
      setOpen(!e.matches)
    }
    handleChange(mql)
    mql.addEventListener("change", handleChange)
    return () => mql.removeEventListener("change", handleChange)
  }, [])

  return [open, setOpen] as const
}
