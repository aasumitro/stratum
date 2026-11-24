import { useState } from "react"
import { useTranslation } from "react-i18next"
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/components/auth-provider"
import {
  pendingConsents,
  useAcceptConsents,
  useConsents,
} from "@/features/account/hooks"
import { ConsentLinks } from "@/features/account/components/consent-links"
import { parseApiError } from "@/lib/api/error"

// Blocks the app until the user accepts the current version of every required
// legal document: shown after a version changes, or to a user who signed up
// before acceptance was recorded. It cannot be dismissed; signing out can. If
// the list cannot be loaded the gate stays hidden: the API does not enforce
// acceptance, so an outage here must not lock everyone out.
export function ConsentGate() {
  const { t } = useTranslation()
  const { data } = useConsents()
  const { accept, isPending } = useAcceptConsents()
  const { signOut } = useAuth()
  const [error, setError] = useState<string | null>(null)
  const pending = pendingConsents(data?.data)
  const pendingKey = pending.map((c) => `${c.document}@${c.version}`).join()
  // Forget a message once everything is accepted, so a later appearance of
  // the gate starts clean. A 409 refreshes the list to newer versions; that
  // message stays until the next click.
  const [shownFor, setShownFor] = useState(pendingKey)
  if (shownFor !== pendingKey) {
    setShownFor(pendingKey)
    if (pendingKey === "") setError(null)
  }

  if (pending.length === 0) return null

  return (
    <AlertDialog open>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("legal.gateTitle")}</AlertDialogTitle>
          <AlertDialogDescription>
            {t("legal.gateDescription")}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <ConsentLinks items={pending} />
        {error && <p className="text-xs text-destructive">{error}</p>}
        <AlertDialogFooter>
          <Button variant="outline" onClick={() => void signOut()}>
            {t("nav.signOut")}
          </Button>
          <Button
            disabled={isPending}
            onClick={() => {
              setError(null)
              accept(pending).catch((err: unknown) =>
                setError(parseApiError(err, t("legal.acceptFailed")))
              )
            }}
          >
            {t("legal.accept")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
