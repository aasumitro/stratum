import { createFileRoute, redirect } from "@tanstack/react-router"
import { resolveLandingRouteSafe } from "@/lib/resolve-landing-organization"
import { RegisterPage } from "@/features/auth/pages/register-page"

export const Route = createFileRoute("/register")({
  beforeLoad: async ({ context }) => {
    if (context.auth.session) {
      throw redirect(await resolveLandingRouteSafe(context.queryClient))
    }
  },
  component: RegisterPage,
})
