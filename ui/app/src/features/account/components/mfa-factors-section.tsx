import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2, IconTrash, IconDeviceMobile } from "@tabler/icons-react"
import type { Factor } from "@supabase/supabase-js"
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
import { Badge } from "@/components/ui/badge"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
import {
  useMfaFactors,
  useEnrollTotpFactor,
  useVerifyTotpFactor,
  useUnenrollMfaFactor,
  useSyncMfaStatus,
} from "@/features/account/hooks"

export function MfaFactorsSection() {
  const { t } = useTranslation()
  const { data: factors, isLoading } = useMfaFactors()
  const { mutate: syncStatus } = useSyncMfaStatus()

  const [totpOpen, setTotpOpen] = useState(false)
  const [totpQrCode, setTotpQrCode] = useState("")
  const [totpSecret, setTotpSecret] = useState("")
  const [totpFactorId, setTotpFactorId] = useState("")
  const [totpCode, setTotpCode] = useState("")
  const [totpError, setTotpError] = useState<string | null>(null)
  const [removeTarget, setRemoveTarget] = useState<Factor | null>(null)

  const { mutate: enrollTotp, isPending: enrollingTotp } = useEnrollTotpFactor()
  const { mutate: verifyTotp, isPending: verifyingTotp } = useVerifyTotpFactor()
  const { mutate: unenroll, isPending: removing } = useUnenrollMfaFactor()

  const verifiedFactors = (factors ?? []).filter((f) => f.status === "verified")

  function handleAddTotp() {
    setTotpError(null)
    enrollTotp(undefined, {
      onSuccess: (data) => {
        setTotpFactorId(data.id)
        // Supabase's qr_code is already a complete `data:image/svg+xml;...`
        // URI on this GoTrue version — verified against the live server;
        // do not re-prepend the prefix (that would double it and break
        // the <img> render).
        setTotpQrCode(data.totp.qr_code)
        setTotpSecret(data.totp.secret)
        setTotpOpen(true)
      },
      onError: () => toast.error(t("settings.mfa.totp.enrollFailed")),
    })
  }

  function handleTotpOpenChange(open: boolean) {
    if (!open && totpFactorId) {
      // Supabase only allows one unverified TOTP factor at a time — closing
      // the dialog before verification must clean it up, or every future
      // enroll attempt fails and the dialog can never open again.
      unenroll(totpFactorId)
      setTotpFactorId("")
      setTotpQrCode("")
      setTotpSecret("")
      setTotpCode("")
      setTotpError(null)
    }
    setTotpOpen(open)
  }

  function handleVerifyTotp() {
    setTotpError(null)
    verifyTotp(
      { factorId: totpFactorId, code: totpCode },
      {
        onSuccess: () => {
          toast.success(t("settings.mfa.totp.enrolled"))
          setTotpFactorId("")
          setTotpOpen(false)
          setTotpCode("")
          syncStatus()
        },
        onError: () => setTotpError(t("settings.mfa.totp.verifyFailed")),
      }
    )
  }

  function handleRemove() {
    if (!removeTarget) return
    unenroll(removeTarget.id, {
      onSuccess: () => {
        toast.success(t("settings.mfa.removed"))
        setRemoveTarget(null)
        syncStatus()
      },
      onError: () => toast.error(t("settings.mfa.removeFailed")),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.mfa.title")}</CardTitle>
        <CardDescription>{t("settings.mfa.description")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {isLoading && (
          <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
        )}
        {!isLoading && verifiedFactors.length === 0 && (
          <p className="text-sm text-muted-foreground">
            {t("settings.mfa.empty")}
          </p>
        )}
        {verifiedFactors.map((factor) => (
          <div
            key={factor.id}
            className="flex items-center justify-between rounded-md border p-3"
          >
            <div className="flex items-center gap-2">
              <IconDeviceMobile className="size-4 text-muted-foreground" />
              <span className="text-sm">
                {factor.friendly_name || t("settings.mfa.totp.label")}
              </span>
              <Badge variant="secondary">{factor.factor_type}</Badge>
            </div>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setRemoveTarget(factor)}
              aria-label={t("settings.mfa.remove")}
            >
              <IconTrash className="size-4" />
            </Button>
          </div>
        ))}
      </CardContent>
      <CardFooter className="flex gap-2">
        <Button
          variant="outline"
          disabled={enrollingTotp}
          onClick={handleAddTotp}
        >
          {enrollingTotp && (
            <IconLoader2 data-icon="inline-start" className="animate-spin" />
          )}
          {t("settings.mfa.totp.add")}
        </Button>
      </CardFooter>

      <Dialog open={totpOpen} onOpenChange={handleTotpOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("settings.mfa.totp.dialogTitle")}</DialogTitle>
            <DialogDescription>
              {t("settings.mfa.totp.dialogDescription")}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col items-center gap-3">
            {totpQrCode && (
              <img
                src={totpQrCode}
                alt={t("settings.mfa.totp.qrAlt")}
                className="size-48"
              />
            )}
            <div className="w-full">
              <Label className="text-xs text-muted-foreground">
                {t("settings.mfa.totp.secretLabel")}
              </Label>
              <p className="rounded-md bg-muted p-2 font-mono text-xs break-all">
                {totpSecret}
              </p>
            </div>
            <div className="flex w-full flex-col gap-1.5">
              <Label htmlFor="totp-code">
                {t("settings.mfa.totp.codeLabel")}
              </Label>
              <Input
                id="totp-code"
                inputMode="numeric"
                maxLength={6}
                value={totpCode}
                onChange={(e) => setTotpCode(e.target.value)}
              />
            </div>
            {totpError && (
              <p className="w-full text-sm text-destructive">{totpError}</p>
            )}
          </div>
          <DialogFooter>
            <Button
              disabled={verifyingTotp || totpCode.length !== 6}
              onClick={handleVerifyTotp}
            >
              {verifyingTotp && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("settings.mfa.totp.verify")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={!!removeTarget}
        onOpenChange={(open) => !open && setRemoveTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("settings.mfa.removeTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("settings.mfa.removeDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="text-destructive-foreground bg-destructive hover:bg-destructive/90"
              disabled={removing}
              onClick={handleRemove}
            >
              {removing && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("settings.mfa.remove")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
