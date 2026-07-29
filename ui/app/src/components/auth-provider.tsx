import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react"
import type { Session, User, AuthResponse } from "@supabase/supabase-js"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { supabase } from "@/lib/auth/supabase"
import { setCookie, deleteCookie } from "@/lib/cookie"
import { queryClient } from "@/lib/query"
import { queryKeys } from "@/lib/api/keys"

interface AuthContextValue {
  session: Session | null
  user: User | null
  loading: boolean
  isPasswordRecovery: boolean
  signIn: (email: string, password: string) => Promise<Session>
  signUp: (email: string, password: string) => Promise<AuthResponse["data"]>
  signOut: () => Promise<void>
  resetPassword: (email: string) => Promise<void>
  updatePassword: (password: string) => Promise<void>
  updateEmail: (email: string) => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [session, setSession] = useState<Session | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)
  const [isPasswordRecovery, setIsPasswordRecovery] = useState(false)
  // Tracks the last known email so we can toast when it actually flips —
  // only fires for a confirmation completed in this same browser tab; a link
  // clicked on another device never reaches this listener. See email-section.tsx
  // for the pending-state UI that covers the other case.
  const lastEmailRef = useRef<string | null>(null)
  // useTranslation's `t` gets a new reference on every language change
  // (react-i18next recomputes it via i18n.getFixedT once i18n.language
  // differs from the last render). Reading it through a ref instead of a
  // dependency keeps the listener effect below mount-only — otherwise a
  // language toggle tears down and resubscribes the Supabase auth
  // listener and refetches the session for a change that has nothing to
  // do with auth.
  const tRef = useRef(t)
  useEffect(() => {
    tRef.current = t
  }, [t])

  useEffect(() => {
    supabase.auth.getSession().then(({ data }) => {
      setSession(data.session)
      setUser(data.session?.user ?? null)
      lastEmailRef.current = data.session?.user?.email ?? null
      syncCookie(data.session)
      setLoading(false)
    })

    const { data: listener } = supabase.auth.onAuthStateChange(
      (event, newSession) => {
        if (event === "PASSWORD_RECOVERY") {
          // Recovery session: set state so the reset-password route is accessible,
          // but don't sync __session — updateUser goes via Supabase SDK directly,
          // not through our Axios/backend, so no API cookie is needed.
          setIsPasswordRecovery(true)
          setSession(newSession)
          setUser(newSession?.user ?? null)
          return
        }

        const newEmail = newSession?.user?.email ?? null
        if (
          lastEmailRef.current &&
          newEmail &&
          newEmail !== lastEmailRef.current
        ) {
          toast.success(
            tRef.current("settings.email.confirmed", { email: newEmail })
          )
          void queryClient.invalidateQueries({
            queryKey: queryKeys.account.me(),
          })
        }
        lastEmailRef.current = newEmail

        setSession(newSession)
        setUser(newSession?.user ?? null)
        syncCookie(newSession)
      }
    )

    return () => listener.subscription.unsubscribe()
  }, [])

  const signIn = useCallback(
    async (email: string, password: string): Promise<Session> => {
      const { data, error } = await supabase.auth.signInWithPassword({
        email,
        password,
      })
      if (error) throw error
      // Sync cookie immediately so the axios interceptor has the token
      // before any subsequent API calls in the same tick.
      syncCookie(data.session)
      return data.session!
    },
    []
  )

  const signUp = useCallback(async (email: string, password: string) => {
    const { data, error } = await supabase.auth.signUp({ email, password })
    if (error) throw error
    return data
  }, [])

  const signOut = useCallback(async () => {
    // Ignore Supabase API errors — the session may already be invalidated server-side
    // (e.g. after /me/sessions/revoke-all). Local cleanup must always happen.
    await supabase.auth.signOut().catch(() => undefined)
    localStorage.removeItem("active_organization_id")
    localStorage.removeItem("post_login_redirect")
    deleteCookie("__session")
    // Query keys aren't scoped by user id — without this, a different
    // account signing in on the same tab could briefly render this
    // account's cached organizations/notifications/billing data.
    queryClient.clear()
  }, [])

  const resetPassword = useCallback(async (email: string) => {
    const { error } = await supabase.auth.resetPasswordForEmail(email, {
      redirectTo: `${window.location.origin}/reset-password`,
    })
    if (error) throw error
  }, [])

  const updatePassword = useCallback(async (password: string) => {
    const { error } = await supabase.auth.updateUser({ password })
    if (error) throw error
    setIsPasswordRecovery(false)
  }, [])

  // Supabase owns the whole request → verify → commit flow (with "Secure
  // email change" requiring both old and new addresses to confirm). This
  // only kicks that off — account.users.email is synced separately, after
  // the fact, by a Supabase Database Webhook.
  const updateEmail = useCallback(async (email: string) => {
    const { error } = await supabase.auth.updateUser({ email })
    if (error) throw error
  }, [])

  const value = useMemo<AuthContextValue>(
    () => ({
      session,
      user,
      loading,
      isPasswordRecovery,
      signIn,
      signUp,
      signOut,
      resetPassword,
      updatePassword,
      updateEmail,
    }),
    [
      session,
      user,
      loading,
      isPasswordRecovery,
      signIn,
      signUp,
      signOut,
      resetPassword,
      updatePassword,
      updateEmail,
    ]
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth must be used within AuthProvider")
  return ctx
}

function syncCookie(session: Session | null) {
  if (session?.access_token) {
    // expires_at is UNIX seconds; fall back to ~1h if absent
    const expMs =
      (session.expires_at ?? Math.floor(Date.now() / 1000) + 3600) * 1000
    const days = Math.max(0, (expMs - Date.now()) / 86_400_000)
    setCookie("__session", session.access_token, days)
  } else {
    deleteCookie("__session")
  }
}
