import { createFileRoute, redirect } from "@tanstack/react-router"
import { ResetPasswordPage } from "@/features/auth/pages/reset-password-page"

export const Route = createFileRoute("/reset-password")({
  beforeLoad: ({ context }) => {
    // Allow access when Supabase fired PASSWORD_RECOVERY — user must complete the form.
    // Redirect only for normal authenticated sessions.
    if (context.auth.session && !context.auth.isPasswordRecovery) {
      throw redirect({ to: "/organizations" })
    }
  },
  component: ResetPasswordPage,
})
