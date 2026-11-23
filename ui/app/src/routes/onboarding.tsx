import { createFileRoute, redirect } from "@tanstack/react-router"
import { getFn } from "@/lib/api/query"
import { API } from "@/lib/api/path"
import { queryKeys } from "@/lib/api/keys"
import { isRefusal } from "@/lib/api/error"
import type { HTTPResponse } from "@/lib/api/response"
import type { UserProfile } from "@/types/account"
import type { OrganizationView } from "@/types/organization"
import { needsMfaChallenge } from "@/lib/auth/mfa"
import { resolveLandingRouteSafe } from "@/lib/resolve-landing-organization"
import { OnboardingPage } from "@/features/onboarding/pages/onboarding-page"

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
      // Only a refusal (no profile yet) means step 1. An outage or a server
      // error would just fail the form again, so show the real problem.
      if (!isRefusal(err)) throw err
    }

    if (profile?.preferences?.onboarding_completed === true) {
      throw redirect(await resolveLandingRouteSafe(context.queryClient))
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
      if (!isRefusal(err)) throw err
      return { initialStep: 2 }
    }
  },
  component: OnboardingPage,
})
