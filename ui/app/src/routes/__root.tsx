import { Outlet, createRootRouteWithContext } from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"
import type { useAuth } from "@/components/auth-provider"
import { NotFoundPage } from "@/components/shared/not-found-page"
import { RouteErrorBoundary } from "@/components/shared/route-error-boundary"

export const Route = createRootRouteWithContext<{
  queryClient: QueryClient
  auth: ReturnType<typeof useAuth>
}>()({
  component: Outlet,
  notFoundComponent: NotFoundPage,
  errorComponent: RouteErrorBoundary,
})
