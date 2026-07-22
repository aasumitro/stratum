import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { useAuth } from "@/components/auth-provider"

export function useSignUp() {
  const { signUp } = useAuth()
  const navigate = useNavigate()
  const { t } = useTranslation()

  // Returns true when email confirmation was sent (session not yet available).
  async function signUpAndRedirect(
    email: string,
    password: string
  ): Promise<boolean> {
    const data = await signUp(email, password)

    // Supabase's anti-enumeration behavior: signUp() against an email that
    // already has a *confirmed* account doesn't return an error — it
    // returns a success-shaped response with an empty `identities` array
    // and no session, and (critically) never actually sends a new
    // confirmation email. Without this check we'd show "check your email"
    // for an email that will never receive anything.
    if (data.user && data.user.identities?.length === 0) {
      throw new Error(t("auth.register.emailAlreadyRegistered"))
    }

    if (!data.session) {
      // Supabase requires email confirmation — stay on register page.
      return true
    }
    await navigate({ to: "/onboarding" })
    return false
  }

  return { signUpAndRedirect }
}
