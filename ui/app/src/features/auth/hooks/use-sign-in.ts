import { useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/components/auth-provider"
import { getFn } from "@/lib/api/query"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { resolveLandingRoute } from "@/lib/resolve-landing-organization"
import type { HTTPResponse } from "@/lib/api/response"
import type { UserProfile } from "@/types/account"

export function useSignIn() {
  const { signIn } = useAuth()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  async function signInAndRedirect(email: string, password: string) {
    await signIn(email, password)

    try {
      const res = await queryClient.fetchQuery<HTTPResponse<UserProfile>>({
        queryKey: queryKeys.account.me(),
        queryFn: getFn<UserProfile>(API.me()),
        staleTime: 0,
      })

      const profile = res.data
      const onboarded =
        profile?.full_name && profile.preferences?.onboarding_completed === true

      if (!onboarded) {
        await navigate({ to: "/onboarding" })
        return
      }

      const pending = localStorage.getItem("post_login_redirect")
      localStorage.removeItem("post_login_redirect")
      if (pending && pending.startsWith("/")) {
        await navigate({ to: pending as never })
        return
      }

      const dest = await resolveLandingRoute(queryClient)
      await navigate(dest as never)
    } catch {
      await navigate({ to: "/onboarding" })
    }
  }

  return { signInAndRedirect }
}
