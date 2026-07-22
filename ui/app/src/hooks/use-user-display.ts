import { useAuth } from "@/components/auth-provider"
import { useProfile } from "@/features/account/hooks"

/** Shared avatar-initials/name display used by both nav trigger variants. */
export function useUserDisplay() {
  const { user } = useAuth()
  const { data } = useProfile()
  const profile = data?.data
  return { profile, email: user?.email }
}
