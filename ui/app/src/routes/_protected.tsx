import { createFileRoute, redirect } from "@tanstack/react-router"
import { getFn } from "@/lib/api/query"
import { API } from "@/lib/api/path"
import { queryKeys } from "@/lib/api/keys"
import { isConnectionError } from "@/lib/api/error"
import type { UserProfile } from "@/types/account"
import { needsMfaChallenge } from "@/lib/auth/mfa"
import { ProtectedLayout } from "@/components/layout/protected-layout"
import { NotFoundPage } from "@/components/shared/not-found-page"
import { ConnectionErrorPage } from "@/components/shared/connection-error-page"

export const Route = createFileRoute("/_protected")({
  beforeLoad: async ({ context }) => {
    if (!context.auth.session) {
      const dest = location.pathname + (location.search ?? "")
      if (dest !== "/" && dest !== "/login")
        localStorage.setItem("post_login_redirect", dest)
      throw redirect({ to: "/login" })
    }

    if (await needsMfaChallenge()) throw redirect({ to: "/mfa-challenge" })

    let onboarded = false
    try {
      const res = await context.queryClient.ensureQueryData({
        queryKey: queryKeys.account.me(),
        queryFn: getFn<UserProfile>(API.me()),
        staleTime: 30_000,
      })
      const profile = res.data
      onboarded = !!(
        profile?.full_name &&
        profile?.preferences?.onboarding_completed === true
      )
    } catch (err) {
      // Can't tell if the user needs onboarding or not — the API is
      // unreachable, not "no profile yet". Don't guess; show the real
      // problem instead of silently dropping them into onboarding.
      if (isConnectionError(err)) throw err
      // Anything else (404 profile not found, etc.) → needs onboarding.
    }

    if (!onboarded) {
      const dest = location.pathname + (location.search ?? "")
      if (dest !== "/" && dest !== "/onboarding" && dest !== "/organizations")
        localStorage.setItem("post_login_redirect", dest)
      throw redirect({ to: "/onboarding" })
    }
  },
  component: ProtectedLayout,
  notFoundComponent: NotFoundPage,
  errorComponent: ConnectionErrorPage,
})
