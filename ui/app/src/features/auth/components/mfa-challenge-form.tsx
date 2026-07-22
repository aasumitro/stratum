import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconLoader2, IconDeviceMobile } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { supabase } from "@/lib/auth/supabase"
import { useMfaFactors } from "@/features/account/hooks"

function postChallengeRedirect(navigate: ReturnType<typeof useNavigate>) {
  const pending = localStorage.getItem("post_login_redirect")
  localStorage.removeItem("post_login_redirect")
  void navigate({ to: (pending as never) ?? "/organizations" })
}

export function MfaChallengeForm() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { data: factors, isLoading } = useMfaFactors()
  const [code, setCode] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [verifying, setVerifying] = useState(false)

  const verified = (factors ?? []).filter((f) => f.status === "verified")
  const totpFactor = verified.find((f) => f.factor_type === "totp")

  async function handleVerifyTotp() {
    if (!totpFactor) return
    setError(null)
    setVerifying(true)
    const { error: err } = await supabase.auth.mfa.challengeAndVerify({
      factorId: totpFactor.id,
      code,
    })
    setVerifying(false)
    if (err) {
      setError(t("auth.mfaChallenge.invalidCode"))
      return
    }
    postChallengeRedirect(navigate)
  }

  return (
    <div className="flex flex-col gap-8">
      <div>
        <h1 className="text-2xl font-extrabold text-foreground">
          {t("auth.mfaChallenge.title")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("auth.mfaChallenge.subtitle")}
        </p>
      </div>

      {isLoading && (
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      )}

      {!isLoading && totpFactor && (
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <IconDeviceMobile className="size-4" />
            {t("auth.mfaChallenge.totpPrompt")}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="mfa-code">{t("auth.mfaChallenge.codeLabel")}</Label>
            <Input
              id="mfa-code"
              inputMode="numeric"
              maxLength={6}
              autoComplete="one-time-code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </div>
          <Button
            disabled={verifying || code.length !== 6}
            onClick={() => void handleVerifyTotp()}
          >
            {verifying && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {t("auth.mfaChallenge.verify")}
          </Button>
        </div>
      )}

      {error && (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}
