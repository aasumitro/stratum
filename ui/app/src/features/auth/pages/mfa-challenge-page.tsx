import { AuthLayout } from "@/features/auth/components/auth-layout"
import { MfaChallengeForm } from "@/features/auth/components/mfa-challenge-form"

export function MfaChallengePage() {
  return (
    <AuthLayout>
      <MfaChallengeForm />
    </AuthLayout>
  )
}
