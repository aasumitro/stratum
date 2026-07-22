import { createFileRoute, redirect } from "@tanstack/react-router"
import { ForgotPasswordPage } from "@/features/auth/pages/forgot-password-page"

export const Route = createFileRoute("/forgot-password")({
  beforeLoad: ({ context }) => {
    if (context.auth.session) throw redirect({ to: "/organizations" })
  },
  component: ForgotPasswordPage,
})
