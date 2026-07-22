import { createFileRoute, redirect } from "@tanstack/react-router"
import { RegisterPage } from "@/features/auth/pages/register-page"

export const Route = createFileRoute("/register")({
  beforeLoad: ({ context }) => {
    if (context.auth.session) throw redirect({ to: "/organizations" })
  },
  component: RegisterPage,
})
