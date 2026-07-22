import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { useHTTPActionPatch } from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { handleHttpError } from "@/lib/api/error"
import type { UserProfile } from "@/types/account"
import type { HTTPResponse } from "@/lib/api/response"

/**
 * Onboarding ends the moment the organization step succeeds (create, join,
 * or accept an invitation) — there's no separate confirmation screen (1c:
 * "on success: toast ... -> land on first-run dashboard"). This marks
 * onboarding_completed and lands on the resulting organization directly.
 */
export function useCompleteOnboarding() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const { mutate, isPending } = useHTTPActionPatch<
    unknown,
    Record<string, unknown>
  >({ url: API.me("preferences") })

  function complete(
    organizationId: string | undefined,
    successMessage: string
  ) {
    mutate(
      { onboarding_completed: true },
      {
        onSuccess: () => {
          // Synchronously write into cache so _protected.tsx's guard sees
          // it immediately (staleTime: 30s would otherwise serve stale).
          queryClient.setQueryData<HTTPResponse<UserProfile>>(
            queryKeys.account.me(),
            (old) => {
              if (!old?.data) return old
              return {
                ...old,
                data: {
                  ...old.data,
                  preferences: {
                    ...(old.data.preferences ?? {}),
                    onboarding_completed: true,
                  },
                },
              }
            }
          )
          // The org switcher/sidebar/"All organizations" page all read
          // queryKeys.organizations.list() — for a first-time user that
          // query was cached empty before this org existed, and nothing
          // else invalidates it after onboarding's create-organization
          // step (unlike the post-onboarding "create another org" sheet,
          // which does). Without this, the org just created shows up
          // fine on its own dashboard (fetched by id) but the switcher
          // and "All organizations" list stay stuck at 0 until staleTime
          // (2 min) expires or something else triggers a refetch.
          void queryClient.invalidateQueries({
            queryKey: queryKeys.organizations.list(),
          })

          toast.success(successMessage)

          const pending = localStorage.getItem("post_login_redirect")
          localStorage.removeItem("post_login_redirect")
          if (pending?.includes("/invitations/accept")) {
            void navigate({ to: pending as never })
          } else if (organizationId) {
            void navigate({
              to: "/organization/$organizationId",
              params: { organizationId },
            })
          } else {
            void navigate({ to: "/organizations" })
          }
        },
        onError: handleHttpError,
      }
    )
  }

  return { complete, isPending }
}
