import { useEffect, useState } from "react"
import {
  IconLayoutGrid,
  IconPlus,
  IconRefresh,
  IconServerOff,
} from "@tabler/icons-react"
import { useNavigate } from "@tanstack/react-router"
import type { Project } from "../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useProjects } from "@/hooks/use-projects"
import { Button } from "@/components/ui/button"
import { DeleteProjectDialog } from "./components/delete-project-dialog"
import { ProjectCard } from "./components/project-card"
import { ProjectFormDrawer } from "./components/project-form-drawer"

function ProjectCardSkeleton() {
  return (
    <div
      className="animate-pulse rounded-lg border bg-card p-5 shadow-sm"
      style={{ borderLeftWidth: 4, borderLeftColor: "var(--border)" }}
    >
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="flex flex-1 items-center gap-2">
          <div className="size-3 rounded-full bg-muted" />
          <div className="h-4 w-28 rounded bg-muted" />
        </div>
        <div className="size-7 rounded bg-muted" />
      </div>
      <div className="mb-3 h-3 w-40 rounded bg-muted" />
      <div className="mb-3 flex items-center gap-1.5">
        <div className="size-1.5 rounded-full bg-muted" />
        <div className="h-3 w-20 rounded bg-muted" />
      </div>
      <div className="mb-3 h-px bg-muted" />
      <div className="flex items-center gap-3">
        <div className="h-3 w-16 rounded bg-muted" />
        <div className="h-3 w-10 rounded bg-muted" />
        <div className="h-3 w-12 rounded bg-muted" />
      </div>
    </div>
  )
}

export function ProjectListPage() {
  const { data: projects, isLoading, error, refetch } = useProjects()

  const navigate = useNavigate()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<Project | undefined>()
  const [deleteTarget, setDeleteTarget] = useState<Project | null>(null)

  const openAdd = () => {
    setEditTarget(undefined)
    setDrawerOpen(true)
  }

  const openEdit = (project: Project) => {
    setEditTarget(project)
    setDrawerOpen(true)
  }

  const handleSelect = (project: Project) => {
    navigate({
      to: "/studio/$projectId/dashboard",
      params: { projectId: project.id },
    })
  }

  // Cmd+N / Ctrl+N → open add project drawer
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "n") {
        e.preventDefault()
        openAdd()
      }
    }
    document.addEventListener("keydown", handler)
    return () => document.removeEventListener("keydown", handler)
  }, [])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Header */}
      <header className="flex items-center justify-between border-b px-6 py-4">
        <div className="flex items-center gap-2">
          <IconLayoutGrid className="size-5 text-muted-foreground" />
          <h1 className="text-sm font-semibold">Projects</h1>
          {projects && projects.length > 0 && (
            <span className="text-xs text-muted-foreground">
              ({projects.length})
            </span>
          )}
        </div>
        <Button size="sm" onClick={openAdd}>
          <IconPlus className="mr-1.5 size-4" />
          Add project
          <kbd className="ml-2 hidden rounded border px-1 py-0.5 text-[10px] opacity-60 sm:inline-flex">
            ⌘N
          </kbd>
        </Button>
      </header>

      {/* Content */}
      <main className="flex-1 overflow-y-auto p-6">
        {isLoading ? (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <ProjectCardSkeleton key={i} />
            ))}
          </div>
        ) : error ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
            <p className="text-sm font-medium text-destructive">
              Failed to load projects
            </p>
            <p className="text-xs text-muted-foreground">{String(error)}</p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <IconRefresh className="size-4" />
              Try again
            </Button>
          </div>
        ) : !projects || projects.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-4 text-center">
            <div className="rounded-full bg-muted p-4">
              <IconServerOff className="size-8 text-muted-foreground" />
            </div>
            <div className="flex flex-col gap-1">
              <p className="text-sm font-medium">No projects yet</p>
              <p className="max-w-xs text-xs text-muted-foreground">
                Add your first Stratum deployment to start managing it from
                Studio.
              </p>
            </div>
            <Button size="sm" onClick={openAdd}>
              <IconPlus className="mr-1.5 size-4" />
              Add project
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {projects.map((project: Project) => (
              <ProjectCard
                key={project.id}
                project={project}
                onEdit={openEdit}
                onDelete={setDeleteTarget}
                onSelect={handleSelect}
              />
            ))}
          </div>
        )}
      </main>

      <ProjectFormDrawer
        open={drawerOpen}
        project={editTarget}
        onClose={() => setDrawerOpen(false)}
      />

      <DeleteProjectDialog
        project={deleteTarget}
        onClose={() => setDeleteTarget(null)}
      />
    </div>
  )
}
