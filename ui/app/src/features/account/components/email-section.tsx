import { useState } from "react"
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useAuth } from "@/components/auth-provider"
import { useProfile } from "@/features/account/hooks"

export function EmailSection() {
  const { t } = useTranslation()
  const { user, updateEmail } = useAuth()
  const { data } = useProfile()
  const profile = data?.data
  const [open, setOpen] = useState(false)
  const [newEmail, setNewEmail] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  // Supabase exposes the unconfirmed target address here while a change is
  // pending — reflects Supabase's own state, not anything we track locally.
  const pendingEmail = user?.new_email

  function handleOpenChange(open: boolean) {
    if (open) setNewEmail("")
    setError(null)
    setOpen(open)
  }

  async function handleSave() {
    setError(null)
    if (!newEmail || newEmail === profile?.email) {
      setError(t("settings.email.invalid"))
      return
    }
    setSaving(true)
    try {
      await updateEmail(newEmail)
      toast.success(t("settings.email.requested"))
      setOpen(false)
    } catch (err) {
      const code = (err as { code?: string })?.code
      setError(
        code === "reauthentication_needed"
          ? t("settings.email.reauthRequired")
          : t("settings.email.saveFailed")
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.email.title")}</CardTitle>
        <CardDescription>{t("settings.email.description")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <p className="text-sm">{profile?.email}</p>
        {pendingEmail && (
          <p className="text-sm text-muted-foreground">
            {t("settings.email.pending", {
              current: profile?.email,
              new: pendingEmail,
            })}
          </p>
        )}
      </CardContent>
      <CardFooter>
        <Button variant="outline" onClick={() => handleOpenChange(true)}>
          {t("settings.email.change")}
        </Button>
      </CardFooter>

      <Dialog open={open} onOpenChange={handleOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("settings.email.dialogTitle")}</DialogTitle>
            <DialogDescription>
              {t("settings.email.dialogDescription")}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-email">{t("settings.email.new")}</Label>
            <Input
              id="new-email"
              type="email"
              value={newEmail}
              onChange={(e) => setNewEmail(e.target.value)}
              autoComplete="email"
            />
            {error && <p className="text-sm text-destructive">{error}</p>}
          </div>
          <DialogFooter>
            <Button
              disabled={saving || !newEmail}
              onClick={() => void handleSave()}
            >
              {saving && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {saving ? t("settings.email.saving") : t("settings.email.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}
