import { createFileRoute, redirect } from "@tanstack/react-router"
import { getFn } from "@/lib/api/query"
import { API } from "@/lib/api/path"
import { queryKeys } from "@/lib/api/keys"
import { isConnectionError } from "@/lib/api/error"
import type { HTTPResponse } from "@/lib/api/response"
import type { UserProfile } from "@/types/account"
import type { OrganizationView } from "@/types/organization"
import { needsMfaChallenge } from "@/lib/auth/mfa"
import { OnboardingPage } from "@/features/onboarding/pages/onboarding-page"
import { ConnectionErrorPage } from "@/components/shared/connection-error-page"

export const Route = createFileRoute("/onboarding")({
  beforeLoad: async ({ context }) => {
    if (!context.auth.session) throw redirect({ to: "/login" })
    if (await needsMfaChallenge()) throw redirect({ to: "/mfa-challenge" })
  },
  loader: async ({ context }): Promise<{ initialStep: 1 | 2 | 3 }> => {
    let profile: UserProfile | null = null
    try {
      const res = await context.queryClient.ensureQueryData<
        HTTPResponse<UserProfile>
      >({
        queryKey: queryKeys.account.me(),
        queryFn: getFn<UserProfile>(API.me()),
        staleTime: 0,
      })
      profile = res.data
    } catch (err) {
      // Can't reach the API at all — don't show a profile form that will
      // just fail to submit. Anything else (no profile yet, etc.) means
      // step 1 is genuinely the right place to start.
      if (isConnectionError(err)) throw err
    }

    if (profile?.preferences?.onboarding_completed === true) {
      throw redirect({ to: "/organizations" })
    }

    if (!profile?.full_name) return { initialStep: 1 }

    // Profile done — check if a organization already exists
    try {
      const wsRes = await context.queryClient.ensureQueryData<
        HTTPResponse<OrganizationView[]>
      >({
        queryKey: queryKeys.organizations.list(),
        queryFn: getFn<OrganizationView[]>(API.organizations()),
        staleTime: 0,
      })
      return { initialStep: (wsRes.data?.length ?? 0) > 0 ? 3 : 2 }
    } catch (err) {
      if (isConnectionError(err)) throw err
      return { initialStep: 2 }
    }
  },
  component: OnboardingPage,
  errorComponent: ConnectionErrorPage,
})
