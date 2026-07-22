import { createFileRoute, redirect } from "@tanstack/react-router"
import { LoginPage } from "@/features/auth/pages/login-page"

export const Route = createFileRoute("/login")({
  beforeLoad: ({ context }) => {
    if (context.auth.session) throw redirect({ to: "/organizations" })
  },
  component: LoginPage,
})
