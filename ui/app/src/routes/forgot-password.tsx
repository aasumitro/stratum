import { createFileRoute, redirect } from "@tanstack/react-router"
import { resolveLandingRouteSafe } from "@/lib/resolve-landing-organization"
import { ForgotPasswordPage } from "@/features/auth/pages/forgot-password-page"

export const Route = createFileRoute("/forgot-password")({
  beforeLoad: async ({ context }) => {
    if (context.auth.session) {
      throw redirect(await resolveLandingRouteSafe(context.queryClient))
    }
  },
  component: ForgotPasswordPage,
})
