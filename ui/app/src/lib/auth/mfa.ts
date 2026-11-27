import { supabase } from "@/lib/auth/supabase"

/**
 * True when the current session must complete an MFA challenge before
 * proceeding — the account has a verified factor requiring aal2, but the
 * current session is still only aal1. Supabase enforces aal1 as the
 * session ceiling for accounts with no factors, so this is false for the
 * overwhelming majority of users who never enrolled MFA.
 */
export async function needsMfaChallenge(): Promise<boolean> {
  const { data, error } =
    await supabase.auth.mfa.getAuthenticatorAssuranceLevel()
  if (error || !data) return false
  return data.nextLevel === "aal2" && data.currentLevel !== data.nextLevel
}

// True when this session has passed an MFA challenge (aal2). Requiring MFA for
// an organization from any other session would shut the caller out.
export async function sessionIsAAL2(): Promise<boolean> {
  const { data, error } =
    await supabase.auth.mfa.getAuthenticatorAssuranceLevel()
  return !error && data?.currentLevel === "aal2"
}
