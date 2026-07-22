import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2 } from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { useAuth } from "@/components/auth-provider"
import {
  useRevokeAllSessions,
  useRecordPasswordChanged,
} from "@/features/account/hooks"
import { SessionsTable } from "@/features/account/components/sessions-table"
import { MfaFactorsSection } from "@/features/account/components/mfa-factors-section"

export function SecuritySection() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { updatePassword, signOut } = useAuth()
  const [newPw, setNewPw] = useState("")
  const [confirmPw, setConfirmPw] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [revokeOpen, setRevokeOpen] = useState(false)

  const { mutate: revokeAll, isPending: revoking } = useRevokeAllSessions()
  const { mutate: recordPasswordChanged } = useRecordPasswordChanged()

  async function handleSave() {
    setError(null)
    if (newPw.length < 8) {
      setError(t("settings.password.tooShort"))
      return
    }
    if (newPw !== confirmPw) {
      setError(t("settings.password.mismatch"))
      return
    }
    setSaving(true)
    try {
      await updatePassword(newPw)
      // Audit trail only — never blocks the success toast if this fails.
      recordPasswordChanged()
      toast.success(t("settings.password.saved"))
      setNewPw("")
      setConfirmPw("")
    } catch (err) {
      const msg =
        err instanceof Error && err.message.includes("session")
          ? t("settings.password.sessionExpired")
          : t("settings.password.saveFailed")
      setError(msg)
    } finally {
      setSaving(false)
    }
  }

  function handleRevokeAll() {
    revokeAll(undefined, {
      onSuccess: async () => {
        toast.success(t("settings.sessions.done"))
        await signOut()
        void navigate({ to: "/login" })
      },
      onError: () => toast.error(t("settings.sessions.failed")),
    })
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="text-xl font-semibold">{t("account.securityTitle")}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("account.securityDescription")}
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("settings.password.title")}</CardTitle>
          <CardDescription>
            {t("settings.password.description")}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-pw">{t("settings.password.new")}</Label>
            <Input
              id="new-pw"
              type="password"
              value={newPw}
              onChange={(e) => setNewPw(e.target.value)}
              autoComplete="new-password"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="confirm-pw">{t("settings.password.confirm")}</Label>
            <Input
              id="confirm-pw"
              type="password"
              value={confirmPw}
              onChange={(e) => setConfirmPw(e.target.value)}
              autoComplete="new-password"
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter>
          <Button
            disabled={saving || !newPw || !confirmPw}
            onClick={() => void handleSave()}
          >
            {saving && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {saving
              ? t("settings.password.saving")
              : t("settings.password.save")}
          </Button>
        </CardFooter>
      </Card>

      <MfaFactorsSection />

      <Card>
        <CardHeader>
          <CardTitle>{t("settings.sessions.title")}</CardTitle>
          <CardDescription>
            {t("settings.sessions.description")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <SessionsTable />
        </CardContent>
        <CardFooter>
          <Button variant="destructive" onClick={() => setRevokeOpen(true)}>
            {t("settings.sessions.signOutAll")}
          </Button>
        </CardFooter>
      </Card>

      <AlertDialog open={revokeOpen} onOpenChange={setRevokeOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("settings.sessions.revokeTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("settings.sessions.revokeDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="text-destructive-foreground bg-destructive hover:bg-destructive/90"
              disabled={revoking}
              onClick={handleRevokeAll}
            >
              {revoking && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("settings.sessions.signOutAll")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
