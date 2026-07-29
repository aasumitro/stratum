import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useForm } from "@tanstack/react-form"
import { toast } from "sonner"
import { IconCheck, IconLoader2, IconPlus } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Avatar, AvatarImage, AvatarFallback } from "@/components/ui/avatar"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useHTTPActionPost } from "@/lib/api/action"
import { API } from "@/lib/api/path"
import { parseApiError } from "@/lib/api/error"
import { useAuth } from "@/components/auth-provider"
import { useTheme } from "@/components/theme-provider"
import { useUpdatePreferences, useUploadAvatar } from "@/features/account/hooks"
import { cn } from "@/lib/ui"
import i18n from "@/lib/i18n"
import type { UserProfile } from "@/types/account"

const detectedTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone

export function StepProfile({ onNext }: { onNext: () => void }) {
  const { t } = useTranslation()
  const { user } = useAuth()
  // ThemeProvider itself falls back to "system" when storage is empty —
  // onboarding has no UI for "system" yet, so treat that as "light".
  const { theme } = useTheme()
  const detectedTheme = theme === "system" ? "light" : theme
  const [serverError, setServerError] = useState<string | null>(null)
  const [avatarFile, setAvatarFile] = useState<File | null>(null)
  const [avatarPreview, setAvatarPreview] = useState<string | null>(null)
  const avatarInputRef = useRef<HTMLInputElement>(null)

  // Avatar can only be uploaded once account.users exists (POST /me below
  // creates that row) — the file is captured here and uploaded right after
  // profile creation succeeds, not on selection.
  const { mutate: uploadAvatar } = useUploadAvatar()

  // display_name + timezone + lang + theme go into preferences (opaque
  // JSON, no backend schema needed) via PATCH /me/preferences, right after
  // the POST /me upsert that persists full_name — two calls, one step.
  const { mutate: savePreferences, isPending: savingPreferences } =
    useUpdatePreferences()

  const { mutate, isPending: savingProfile } = useHTTPActionPost<
    UserProfile,
    { email: string; full_name: string }
  >({
    url: API.me(),
    options: {
      onSuccess: () => {
        const proceed = () =>
          savePreferences(
            {
              display_name: form.state.values.display_name.trim(),
              timezone: detectedTimezone,
              lang: form.state.values.language,
              theme: detectedTheme,
            },
            { onSuccess: () => onNext() }
          )
        // Avatar is optional — a failed upload shouldn't block onboarding.
        if (avatarFile) uploadAvatar(avatarFile, { onSettled: proceed })
        else proceed()
      },
      onError: (err) =>
        setServerError(
          parseApiError(err, t("onboarding.profile.nameRequired"))
        ),
    },
  })

  const isPending = savingProfile || savingPreferences

  const form = useForm({
    defaultValues: {
      full_name: "",
      display_name: "",
      language: i18n.language === "id" ? "id" : "en",
    },
    onSubmit: ({ value }) => {
      setServerError(null)
      mutate({
        email: user?.email ?? "",
        full_name: value.full_name.trim(),
      })
    },
  })

  function handleAvatarChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ""
    if (!file) return

    if (file.size > 2 * 1024 * 1024) {
      toast.error(t("account.avatarTooLarge"))
      return
    }
    if (!file.type.startsWith("image/")) {
      toast.error(t("account.avatarInvalidType"))
      return
    }

    setAvatarFile(file)
    setAvatarPreview(URL.createObjectURL(file))
  }

  return (
    <div className="mx-auto flex w-full max-w-lg flex-col gap-6">
      <div>
        <h2 className="text-xl font-extrabold">
          {t("onboarding.profile.title")}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("onboarding.profile.subtitle")}
        </p>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          void form.handleSubmit()
        }}
        className="flex flex-col gap-4"
      >
        <button
          type="button"
          className="flex items-center gap-3.5 self-start text-left"
          onClick={() => avatarInputRef.current?.click()}
        >
          <Avatar
            size="lg"
            className="size-14 border-2 border-dashed border-border bg-transparent"
          >
            {avatarPreview && <AvatarImage src={avatarPreview} />}
            <AvatarFallback className="bg-transparent">
              <IconPlus className="size-5 text-muted-foreground" />
            </AvatarFallback>
          </Avatar>
          <input
            ref={avatarInputRef}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={handleAvatarChange}
          />
          <div className="flex flex-col gap-0.5">
            <span className="text-sm font-semibold">
              {t("onboarding.profile.avatarUrl")}
            </span>
            <span className="text-xs text-muted-foreground">
              {t("onboarding.profile.avatarHint")}
            </span>
          </div>
        </button>

        <form.Field
          name="full_name"
          validators={{
            onChange: ({ value }) =>
              !value.trim() ? t("onboarding.profile.nameRequired") : undefined,
          }}
        >
          {(field) => {
            const hasError =
              field.state.meta.isTouched && field.state.meta.errors.length > 0
            const valid = field.state.value.trim().length > 0 && !hasError
            return (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="full_name">
                  {t("onboarding.profile.fullName")}{" "}
                  <span className="text-destructive">*</span>
                </Label>
                <div className="relative">
                  <Input
                    id="full_name"
                    placeholder={t("onboarding.profile.fullNamePlaceholder")}
                    aria-invalid={hasError}
                    className={cn(valid && "pr-8")}
                    value={field.state.value}
                    onBlur={field.handleBlur}
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                  {valid && (
                    <IconCheck className="absolute top-1/2 right-2.5 size-4 -translate-y-1/2 text-emerald-600" />
                  )}
                </div>
                {hasError && (
                  <p className="text-xs text-destructive">
                    {field.state.meta.errors[0]}
                  </p>
                )}
              </div>
            )
          }}
        </form.Field>

        <form.Field
          name="display_name"
          validators={{
            onChange: ({ value }) => {
              if (value.trim().length < 2)
                return t("onboarding.profile.displayNameMinLength")
              if (!/^[A-Za-z0-9._]+$/.test(value))
                return t("onboarding.profile.displayNameInvalid")
              return undefined
            },
          }}
        >
          {(field) => {
            const hasError =
              field.state.meta.isTouched && field.state.meta.errors.length > 0
            return (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="display_name">
                  {t("onboarding.profile.displayName")}{" "}
                  <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="display_name"
                  placeholder={t("onboarding.profile.displayNamePlaceholder")}
                  aria-invalid={hasError}
                  value={field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(e.target.value)}
                />
                {hasError && (
                  <p className="text-xs text-destructive">
                    {field.state.meta.errors[0]}
                  </p>
                )}
              </div>
            )
          }}
        </form.Field>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label>{t("onboarding.profile.timezone")}</Label>
            <div className="flex h-9 items-center truncate rounded-lg border bg-muted/40 px-3 text-sm text-muted-foreground">
              {detectedTimezone}
            </div>
            <span className="text-xs text-muted-foreground">
              {t("onboarding.profile.timezoneAutoDetectedCaption")}
            </span>
          </div>
          <form.Field name="language">
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label>{t("onboarding.profile.language")}</Label>
                <Select
                  value={field.state.value}
                  onValueChange={(v) => {
                    const lang = v ?? "en"
                    field.handleChange(lang)
                    // Switch immediately so the rest of the wizard reflects
                    // the choice right away, instead of waiting for submit.
                    if (lang !== i18n.language) void i18n.changeLanguage(lang)
                  }}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="en">{t("language.en")}</SelectItem>
                    <SelectItem value="id">{t("language.id")}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}
          </form.Field>
        </div>

        {serverError && (
          <p
            role="alert"
            className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {serverError}
          </p>
        )}

        <form.Subscribe
          selector={(s) =>
            s.values.full_name.trim().length > 0 &&
            s.values.display_name.trim().length >= 2
          }
        >
          {(valid) => (
            <Button
              type="submit"
              disabled={!valid || isPending}
              className="mt-2 w-full"
            >
              {isPending && (
                <IconLoader2 className="mr-2 size-4 animate-spin" />
              )}
              {t("common.continue")}
            </Button>
          )}
        </form.Subscribe>
      </form>
    </div>
  )
}
