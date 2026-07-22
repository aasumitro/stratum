import {
  Outlet,
  useNavigate,
  useParams,
} from "@tanstack/react-router"
import {
  IconActivity,
  IconBrandDatabricks,
  IconBroadcast,
  IconBuildingCommunity,
  IconChevronDown,
  IconClipboardList,
  IconHeadset,
  IconHistory,
  IconLayoutDashboard,
  IconLayersLinked,
  IconLayoutGrid,
  IconMailExclamation,
  IconRadar,
  IconTag,
} from "@tabler/icons-react"
import { useProjects } from "@/hooks/use-projects"
import { useLastStatus } from "@/hooks/use-monitor"
import { useWatchlist, urgentCount } from "@/hooks/use-watchlist"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Separator } from "@/components/ui/separator"
import { NavLink, NavSection } from "./nav-link"
import { monitorStatusDot } from "./utils"

export function StudioLayout() {
  const { projectId } = useParams({ from: "/studio/$projectId" })
  const navigate = useNavigate()
  const { data: projects } = useProjects()
  const currentProject = projects?.find((p) => p.id === projectId)
  const otherProjects = projects?.filter((p) => p.id !== projectId) ?? []
  const { data: lastMonitorStatus } = useLastStatus(projectId)
  const { data: watchlistData } = useWatchlist(projectId)
  const watchlistUrgent = urgentCount(watchlistData)

  return (
    <div className="flex flex-1 min-h-0">
      {/* Sidebar */}
      <aside className="flex flex-col w-[220px] flex-shrink-0 border-r bg-card/40">
        {/* Project switcher */}
        <div className="px-3 py-[16.4px]">
          <DropdownMenu>
            <DropdownMenuTrigger
              className="flex items-center gap-2 w-full rounded-lg px-2.5 py-2 text-sm hover:bg-accent transition-colors text-left outline-none"
              aria-label="Switch project"
            >
              <span
                className="size-2.5 rounded-full flex-shrink-0"
                style={{ backgroundColor: currentProject?.color ?? "#6366f1" }}
              />
              <span className="flex-1 truncate font-medium text-sm">
                {currentProject?.name ?? "Loading…"}
              </span>
              <IconChevronDown className="size-3.5 text-muted-foreground flex-shrink-0" />
            </DropdownMenuTrigger>

            <DropdownMenuContent align="start" className="w-[200px]">
              {otherProjects.length > 0 ? (
                <>
                  {otherProjects.map((p) => (
                    <DropdownMenuItem
                      key={p.id}
                      onClick={() =>
                        navigate({
                          to: "/studio/$projectId/dashboard",
                          params: { projectId: p.id },
                        })
                      }
                    >
                      <span
                        className="size-2 rounded-full flex-shrink-0"
                        style={{ backgroundColor: p.color }}
                      />
                      <span className="truncate">{p.name}</span>
                    </DropdownMenuItem>
                  ))}
                  <DropdownMenuSeparator />
                </>
              ) : null}
              <DropdownMenuItem onClick={() => navigate({ to: "/" })}>
                <IconLayoutGrid className="size-4" />
                Manage projects
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <Separator />

        {/* Navigation */}
        <nav className="flex flex-col gap-1 p-3 flex-1">
          <NavLink
            to="/studio/$projectId/dashboard"
            params={{ projectId }}
            icon={<IconLayoutDashboard className="size-4" />}
            label="Dashboard"
          />

          <NavSection>Customer Operations</NavSection>
          <NavLink
            to="/studio/$projectId/support"
            params={{ projectId }}
            icon={<IconHeadset className="size-4" />}
            label="Support"
          />
          <NavLink
            to="/studio/$projectId/watchlist"
            params={{ projectId }}
            icon={<IconRadar className="size-4" />}
            label="Watchlist"
            badge={
              watchlistUrgent > 0 ? (
                <span className="inline-flex items-center justify-center min-w-[18px] h-[18px] px-1 rounded-full text-[10px] font-semibold bg-red-500 text-white">
                  {watchlistUrgent}
                </span>
              ) : undefined
            }
          />
          <NavLink
            to="/studio/$projectId/organizations"
            params={{ projectId }}
            icon={<IconBuildingCommunity className="size-4" />}
            label="Organizations"
          />
          <NavLink
            to="/studio/$projectId/broadcast"
            params={{ projectId }}
            icon={<IconBroadcast className="size-4" />}
            label="Broadcast"
          />

          <NavSection>Catalog &amp; Reference Data</NavSection>
          <NavLink
            to="/studio/$projectId/catalog"
            params={{ projectId }}
            icon={<IconTag className="size-4" />}
            label="Catalog"
          />
          <NavLink
            to="/studio/$projectId/references"
            params={{ projectId }}
            icon={<IconLayersLinked className="size-4" />}
            label="References"
          />

          <NavSection>Audit &amp; Logs</NavSection>
          <NavLink
            to="/studio/$projectId/audit"
            params={{ projectId }}
            icon={<IconClipboardList className="size-4" />}
            label="Audit Log"
          />
          <NavLink
            to="/studio/$projectId/operator-log"
            params={{ projectId }}
            icon={<IconHistory className="size-4" />}
            label="Operator Log"
          />

          <NavSection>Infrastructure</NavSection>
          <NavLink
            to="/studio/$projectId/monitoring"
            params={{ projectId }}
            icon={<IconActivity className="size-4" />}
            label="Monitoring"
            badge={monitorStatusDot(lastMonitorStatus?.status)}
          />
          <NavLink
            to="/studio/$projectId/dlq"
            params={{ projectId }}
            icon={<IconMailExclamation className="size-4" />}
            label="Dead Letter Queue"
          />
        </nav>

        {/* Bottom */}
        <div className="p-3 border-t">
          <Button
            variant="ghost"
            className="w-full justify-start gap-2 text-muted-foreground text-sm"
            onClick={() => navigate({ to: "/" })}
          >
            <IconBrandDatabricks className="size-4" />
            All Projects
          </Button>
        </div>
      </aside>

      {/* Content */}
      <main className="flex-1 overflow-y-auto">
        <Outlet />
      </main>
    </div>
  )
}
