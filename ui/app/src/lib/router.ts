import { createRouter } from "@tanstack/react-router"
import { routeTree } from "@/router.gen"
import { queryClient } from "./query"
import { AppLoader } from "@/components/layout/app-loader"

export const router = createRouter({
  routeTree,
  context: {
    queryClient,
    auth: undefined!,
  },
  scrollRestoration: true,
  defaultPreload: "intent",
  defaultPendingComponent: AppLoader,
})

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}
