import { useEffect, useState } from "react"
import { IconLayoutGrid, IconPlus, IconRefresh, IconServerOff } from "@tabler/icons-react"
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
      <div className="flex items-start justify-between gap-2 mb-3">
        <div className="flex items-center gap-2 flex-1">
          <div className="size-3 rounded-full bg-muted" />
          <div className="h-4 bg-muted rounded w-28" />
        </div>
        <div className="size-7 rounded bg-muted" />
      </div>
      <div className="h-3 bg-muted rounded w-40 mb-3" />
      <div className="flex items-center gap-1.5 mb-3">
        <div className="size-1.5 rounded-full bg-muted" />
        <div className="h-3 bg-muted rounded w-20" />
      </div>
      <div className="h-px bg-muted mb-3" />
      <div className="flex items-center gap-3">
        <div className="h-3 bg-muted rounded w-16" />
        <div className="h-3 bg-muted rounded w-10" />
        <div className="h-3 bg-muted rounded w-12" />
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
    <div className="flex flex-col flex-1 min-h-0">
      {/* Header */}
      <header className="flex items-center justify-between px-6 py-4 border-b">
        <div className="flex items-center gap-2">
          <IconLayoutGrid className="size-5 text-muted-foreground" />
          <h1 className="font-semibold text-sm">Projects</h1>
          {projects && projects.length > 0 && (
            <span className="text-xs text-muted-foreground">
              ({projects.length})
            </span>
          )}
        </div>
        <Button size="sm" onClick={openAdd}>
          <IconPlus className="size-4 mr-1.5" />
          Add project
          <kbd className="ml-2 hidden sm:inline-flex text-[10px] opacity-60 border rounded px-1 py-0.5">
            ⌘N
          </kbd>
        </Button>
      </header>

      {/* Content */}
      <main className="flex-1 overflow-y-auto p-6">
        {isLoading ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 3 }).map((_, i) => (
              <ProjectCardSkeleton key={i} />
            ))}
          </div>
        ) : error ? (
          <div className="flex flex-col items-center justify-center h-full gap-3 text-center">
            <p className="text-sm text-destructive font-medium">Failed to load projects</p>
            <p className="text-xs text-muted-foreground">{String(error)}</p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <IconRefresh className="size-4" />
              Try again
            </Button>
          </div>
        ) : !projects || projects.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full gap-4 text-center">
            <div className="rounded-full bg-muted p-4">
              <IconServerOff className="size-8 text-muted-foreground" />
            </div>
            <div className="flex flex-col gap-1">
              <p className="font-medium text-sm">No projects yet</p>
              <p className="text-xs text-muted-foreground max-w-xs">
                Add your first Stratum deployment to start managing it from Studio.
              </p>
            </div>
            <Button size="sm" onClick={openAdd}>
              <IconPlus className="size-4 mr-1.5" />
              Add project
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
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
