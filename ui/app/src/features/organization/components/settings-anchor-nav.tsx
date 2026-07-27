import { useEffect, useState } from "react"
import { cn } from "@/lib/ui"

interface AnchorItem {
  id: string
  label: string
}

interface SettingsAnchorNavProps {
  items: AnchorItem[]
}

/**
 * In-page jump nav for the Settings page's sections. Plain hash anchors —
 * native browser scrolling (which respects each section's `scroll-mt-*` and
 * the page's `motion-safe:scroll-smooth`) handles positioning. An
 * IntersectionObserver only tracks which section is current, for the
 * active-pill highlight (mirrors `RouteTabs`' `bg-primary` active style) —
 * it never drives scrolling itself.
 */
export function SettingsAnchorNav({ items }: SettingsAnchorNavProps) {
  const [activeId, setActiveId] = useState(items[0]?.id)

  useEffect(() => {
    const sections = items
      .map((item) => document.getElementById(item.id))
      .filter((el): el is HTMLElement => el !== null)
    if (!sections.length) return

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (visible[0]) setActiveId(visible[0].target.id)
      },
      { rootMargin: "-96px 0px -70% 0px", threshold: 0 }
    )
    sections.forEach((el) => observer.observe(el))
    return () => observer.disconnect()
    // items' identity changes every render (new array literal) — re-running
    // this effect on every item content change (not just id set) is fine
    // since the section ids themselves rarely change during a page's life.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items.map((i) => i.id).join(",")])

  return (
    <nav
      aria-label="Settings sections"
      className="sticky top-0 z-10 flex gap-1 overflow-x-auto border-b border-border bg-background/95 px-0 py-2 whitespace-nowrap backdrop-blur-sm md:hidden"
    >
      {items.map((item) => (
        <a
          key={item.id}
          href={`#${item.id}`}
          className={cn(
            "rounded-lg px-3 py-1.5 text-sm font-medium transition-colors",
            activeId === item.id
              ? "bg-primary text-primary-foreground"
              : "text-muted-foreground hover:bg-accent hover:text-foreground"
          )}
        >
          {item.label}
        </a>
      ))}
    </nav>
  )
}
