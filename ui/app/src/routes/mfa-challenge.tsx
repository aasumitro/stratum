import { createFileRoute, redirect } from "@tanstack/react-router"
import { needsMfaChallenge } from "@/lib/auth/mfa"
import { MfaChallengePage } from "@/features/auth/pages/mfa-challenge-page"

export const Route = createFileRoute("/mfa-challenge")({
  beforeLoad: async ({ context }) => {
    if (!context.auth.session) throw redirect({ to: "/login" })
    if (!(await needsMfaChallenge())) throw redirect({ to: "/organizations" })
  },
  component: MfaChallengePage,
})
