import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
} from "@tanstack/react-router"
import { ProjectListPage } from "./pages/projects"
import { StudioLayout } from "./pages/studio/layout"
import { DashboardPage } from "./pages/studio/dashboard"
import { DLQPage } from "./pages/studio/dlq"
import { ReferencesPage } from "./pages/studio/references"
import { CatalogPage } from "./pages/studio/catalog"
import { MonitoringPage } from "./pages/studio/monitoring"
import { SupportPage } from "./pages/studio/support"
import { BroadcastPage } from "./pages/studio/broadcast"
import { OrganizationsPage } from "./pages/studio/organizations"
import { AuditPage } from "./pages/studio/audit"
import { OperatorLogPage } from "./pages/studio/operator-log"
import { WatchlistPage } from "./pages/studio/watchlist"

const rootRoute = createRootRoute({
  component: Outlet,
})

const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: ProjectListPage,
})

const studioLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/studio/$projectId",
  component: StudioLayout,
})

const dashboardRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "dashboard",
  component: DashboardPage,
})

const dlqRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "dlq",
  component: DLQPage,
})

const referencesRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "references",
  component: ReferencesPage,
})

const catalogRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "catalog",
  component: CatalogPage,
})

const monitoringRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "monitoring",
  component: MonitoringPage,
})

const supportRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "support",
  component: SupportPage,
})

const broadcastRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "broadcast",
  component: BroadcastPage,
})

const organizationsRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "organizations",
  component: OrganizationsPage,
})

const auditRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "audit",
  component: AuditPage,
})

const operatorLogRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "operator-log",
  component: OperatorLogPage,
})

const watchlistRoute = createRoute({
  getParentRoute: () => studioLayoutRoute,
  path: "watchlist",
  component: WatchlistPage,
})

const routeTree = rootRoute.addChildren([
  projectsRoute,
  studioLayoutRoute.addChildren([dashboardRoute, dlqRoute, referencesRoute, catalogRoute, monitoringRoute, supportRoute, broadcastRoute, organizationsRoute, auditRoute, operatorLogRoute, watchlistRoute]),
])

export const router = createRouter({ routeTree })

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}
