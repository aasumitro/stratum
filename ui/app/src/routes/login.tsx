import { createFileRoute, redirect } from "@tanstack/react-router"
import { resolveLandingRouteSafe } from "@/lib/resolve-landing-organization"
import { LoginPage } from "@/features/auth/pages/login-page"

export const Route = createFileRoute("/login")({
  beforeLoad: async ({ context }) => {
    if (context.auth.session) {
      throw redirect(await resolveLandingRouteSafe(context.queryClient))
    }
  },
  component: LoginPage,
})
