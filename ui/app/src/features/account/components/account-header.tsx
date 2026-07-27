import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconCopy, IconTrash, IconUpload } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Avatar, AvatarImage, AvatarFallback } from "@/components/ui/avatar"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { useAuth } from "@/components/auth-provider"
import {
  useProfile,
  useUploadAvatar,
  useDeleteAvatar,
} from "@/features/account/hooks"
import { handleHttpError } from "@/lib/api/error"
import { initials } from "@/lib/format"

// The one identity anchor for the whole /account section — rendered once
// above the tab nav (not per-tab) so avatar/name/email stay visible and
// don't reset or re-fetch while switching tabs.
export function AccountHeader() {
  const { t, i18n } = useTranslation()
  const { user } = useAuth()
  const { data: profileData } = useProfile()
  const profile = profileData?.data
  const uploadAvatar = useUploadAvatar()
  const deleteAvatar = useDeleteAvatar()
  const inputRef = useRef<HTMLInputElement>(null)
  const [preview, setPreview] = useState<string | null>(null)

  const avatarInitials = initials(profile?.full_name || "?")
  const avatarSrc = preview ?? (profile?.avatar_url || undefined)
  const isPending = uploadAvatar.isPending || deleteAvatar.isPending
  const accountId = user?.id ?? ""

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return

    if (file.size > 2 * 1024 * 1024) {
      toast.error(t("account.avatarTooLarge"))
      return
    }
    if (!file.type.startsWith("image/")) {
      toast.error(t("account.avatarInvalidType"))
      return
    }

    setPreview(URL.createObjectURL(file))
    uploadAvatar.mutate(file, {
      onSuccess: () => toast.success(t("account.avatarUploaded")),
      onError: (err) => {
        setPreview(null)
        handleHttpError(err)
      },
    })

    e.target.value = ""
  }

  function handleDelete() {
    deleteAvatar.mutate(undefined, {
      onSuccess: () => {
        setPreview(null)
        toast.success(t("account.avatarRemoved"))
      },
      onError: handleHttpError,
    })
  }

  const memberSince = profile
    ? new Date(profile.created_at).toLocaleDateString(i18n.language, {
        month: "long",
        year: "numeric",
      })
    : null

  return (
    <div className="flex flex-col gap-4 rounded-xl border bg-muted/40 p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex items-center gap-4">
        <Avatar size="lg" className="size-16 shrink-0">
          <AvatarImage src={avatarSrc} alt={profile?.full_name} />
          <AvatarFallback className="text-lg">{avatarInitials}</AvatarFallback>
        </Avatar>

        <div className="flex min-w-0 flex-col gap-0.5">
          <p className="truncate text-lg font-semibold">
            {profile?.full_name || " "}
          </p>
          <p className="truncate text-sm text-muted-foreground">
            {profile?.email}
          </p>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
            {memberSince && (
              <span>{t("account.memberSince", { date: memberSince })}</span>
            )}
            {accountId && (
              <>
                <span className="opacity-50">·</span>
                <button
                  type="button"
                  title={t("account.accountIdDescription")}
                  onClick={() => {
                    void navigator.clipboard.writeText(accountId)
                    toast.success(t("account.accountIdCopied"))
                  }}
                  className="inline-flex items-center gap-1 rounded font-mono transition-colors hover:text-foreground"
                >
                  {accountId.slice(0, 8)}...
                  <IconCopy
                    className="size-3"
                    aria-label={t("account.copyAccountId")}
                  />
                </button>
              </>
            )}
          </div>
        </div>
      </div>

      <div className="flex shrink-0 gap-2">
        <input
          ref={inputRef}
          type="file"
          accept="image/*"
          className="hidden"
          onChange={handleFileChange}
        />
        <Button
          variant="outline"
          size="sm"
          disabled={isPending}
          onClick={() => inputRef.current?.click()}
        >
          <IconUpload className="mr-1.5 size-4" />
          {t("account.avatarUpload")}
        </Button>

        {avatarSrc && (
          <AlertDialog>
            <AlertDialogTrigger
              render={
                <Button variant="outline" size="sm" disabled={isPending} />
              }
            >
              <IconTrash className="mr-1.5 size-4" />
              {t("account.avatarRemove")}
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>
                  {t("account.avatarRemoveTitle")}
                </AlertDialogTitle>
                <AlertDialogDescription>
                  {t("account.avatarRemoveDescription")}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
                <AlertDialogAction
                  className="text-destructive-foreground bg-destructive hover:bg-destructive/90"
                  onClick={handleDelete}
                >
                  {t("account.avatarRemove")}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        )}
      </div>
    </div>
  )
}
