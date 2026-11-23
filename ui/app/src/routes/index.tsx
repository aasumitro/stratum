import { createFileRoute, isRedirect, redirect } from "@tanstack/react-router"
import { resolveLandingRoute } from "@/lib/resolve-landing-organization"
import { isRefusal } from "@/lib/api/error"

export const Route = createFileRoute("/")({
  beforeLoad: async ({ context }) => {
    if (!context.auth.session) throw redirect({ to: "/login" })
    try {
      const dest = await resolveLandingRoute(context.queryClient)
      throw redirect(dest as never)
    } catch (err) {
      // A redirect() is thrown intentionally above — let it propagate.
      if (isRedirect(err)) throw err
      // Only a refusal says the profile doesn't exist yet; anything else
      // (server down, a 5xx) is not an answer.
      if (!isRefusal(err)) throw err
      // No profile (fresh signup, just confirmed email) → onboarding.
      throw redirect({ to: "/onboarding" })
    }
  },
  component: () => null,
})
