import { createFileRoute, isRedirect, redirect } from "@tanstack/react-router"
import { resolveLandingRoute } from "@/lib/resolve-landing-organization"
import { isConnectionError } from "@/lib/api/error"
import { ConnectionErrorPage } from "@/components/shared/connection-error-page"

export const Route = createFileRoute("/")({
  beforeLoad: async ({ context }) => {
    if (!context.auth.session) throw redirect({ to: "/login" })
    try {
      const dest = await resolveLandingRoute(context.queryClient)
      throw redirect(dest as never)
    } catch (err) {
      // A redirect() is thrown intentionally above — let it propagate.
      if (isRedirect(err)) throw err
      // Can't tell if the profile just doesn't exist yet vs. server down.
      if (isConnectionError(err)) throw err
      // No profile (fresh signup, just confirmed email) → onboarding.
      throw redirect({ to: "/onboarding" })
    }
  },
  component: () => null,
  errorComponent: ConnectionErrorPage,
})
