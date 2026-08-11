import { useEffect, useRef, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { IconSearch } from "@tabler/icons-react"
import type { Project } from "../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useProjects } from "@/hooks/use-projects"

interface CommandPaletteProps {
  open: boolean
  onClose: () => void
}

export function CommandPalette({ open, onClose }: CommandPaletteProps) {
  const [query, setQuery] = useState("")
  const [wasOpen, setWasOpen] = useState(open)
  const { data: projects } = useProjects()
  const navigate = useNavigate()
  const inputRef = useRef<HTMLInputElement>(null)

  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setQuery("")
  }

  useEffect(() => {
    if (open) {
      // Defer focus so the element is visible first
      const id = setTimeout(() => inputRef.current?.focus(), 30)
      return () => clearTimeout(id)
    }
  }, [open])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    if (open) {
      document.addEventListener("keydown", handler)
      return () => document.removeEventListener("keydown", handler)
    }
  }, [open, onClose])

  const filtered = (projects ?? []).filter(
    (p) =>
      !query ||
      p.name.toLowerCase().includes(query.toLowerCase()) ||
      p.api_url.toLowerCase().includes(query.toLowerCase())
  )

  const select = (project: Project) => {
    onClose()
    navigate({
      to: "/studio/$projectId/dashboard",
      params: { projectId: project.id },
    })
  }

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center px-4 pt-20">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />

      {/* Panel */}
      <div className="relative w-full max-w-lg overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-2xl">
        {/* Search input */}
        <div className="flex items-center gap-3 border-b px-4 py-3">
          <IconSearch className="size-4 shrink-0 text-muted-foreground" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search projects…"
            className="flex-1 bg-transparent text-sm outline-hidden placeholder:text-muted-foreground"
          />
          <kbd className="hidden items-center rounded border px-1.5 py-0.5 text-[10px] text-muted-foreground sm:inline-flex">
            esc
          </kbd>
        </div>

        {/* Results */}
        <div className="max-h-72 overflow-y-auto">
          {filtered.length === 0 ? (
            <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
              No projects found
            </div>
          ) : (
            <ul>
              {filtered.map((p) => (
                <li key={p.id}>
                  <button
                    onClick={() => select(p)}
                    className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-accent hover:text-accent-foreground"
                  >
                    <span
                      className="size-2.5 shrink-0 rounded-full"
                      style={{ backgroundColor: p.color }}
                    />
                    <div className="flex min-w-0 flex-col gap-0.5">
                      <span className="truncate text-sm font-medium">
                        {p.name}
                      </span>
                      <span className="truncate text-xs text-muted-foreground">
                        {p.api_url}
                      </span>
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* Footer hint */}
        <div className="flex items-center gap-3 border-t px-4 py-2 text-[10px] text-muted-foreground">
          <span>↵ open project</span>
          <span>esc close</span>
        </div>
      </div>
    </div>
  )
}
