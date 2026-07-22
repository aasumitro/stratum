import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconSun, IconMoon, IconDeviceLaptop } from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useTheme } from "@/components/theme-provider"
import { useProfile, useUpdatePreferences } from "@/features/account/hooks"
import { cn } from "@/lib/ui"
import i18n from "@/lib/i18n"

type Theme = "light" | "dark" | "system"

const THEMES: { value: Theme; icon: React.ElementType; key: string }[] = [
  { value: "light", icon: IconSun, key: "theme.light" },
  { value: "dark", icon: IconMoon, key: "theme.dark" },
  { value: "system", icon: IconDeviceLaptop, key: "theme.system" },
]

const LANGUAGES = [
  { value: "en", key: "language.en" },
  { value: "id", key: "language.id" },
] as const

const detectedTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone
const TIMEZONES = Intl.supportedValuesOf("timeZone")

// All options visible at once (radio-group semantics, not a cycling
// button) — every change saves instantly with a toast, no Save button (AC1).
export function PreferencesSection() {
  const { t, i18n: i18nHook } = useTranslation()
  const { theme, setTheme } = useTheme()
  const { data: profileData } = useProfile()
  const { mutate: updatePreferences } = useUpdatePreferences()
  const current = i18nHook.language
  const timezone =
    (profileData?.data?.preferences?.timezone as string | undefined) ??
    detectedTimezone

  function handleTheme(value: Theme) {
    setTheme(value)
    updatePreferences(
      { theme: value },
      { onSuccess: () => toast.success(t("account.preferencesSaved")) }
    )
  }

  function changeLang(lang: string) {
    void i18n.changeLanguage(lang)
    localStorage.setItem("lang", lang)
    updatePreferences(
      { lang },
      { onSuccess: () => toast.success(t("account.preferencesSaved")) }
    )
  }

  function changeTimezone(tz: string) {
    updatePreferences(
      { timezone: tz },
      { onSuccess: () => toast.success(t("account.preferencesSaved")) }
    )
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="text-xl font-semibold">
          {t("account.preferencesTitle")}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("account.preferencesDescription")}
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("settings.appearance.title")}</CardTitle>
          <CardDescription>
            {t("settings.appearance.description")}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          <div
            role="radiogroup"
            aria-label={t("settings.appearance.title")}
            className="flex gap-2"
          >
            {THEMES.map(({ value, icon: Icon, key }) => (
              <button
                key={value}
                role="radio"
                aria-checked={theme === value}
                onClick={() => handleTheme(value)}
                className={cn(
                  "flex flex-1 flex-col items-center gap-2 rounded-xl border p-4 text-xs font-medium transition-colors",
                  theme === value
                    ? "border-primary bg-primary/5 text-primary"
                    : "text-muted-foreground hover:bg-accent hover:text-foreground"
                )}
              >
                <Icon className="size-5" />
                {t(key)}
              </button>
            ))}
          </div>

          <div className="flex items-center justify-between gap-4">
            <div>
              <p className="text-sm font-medium">
                {t("settings.language.title")}
              </p>
              <p className="text-xs text-muted-foreground">
                {t("settings.language.description")}
              </p>
            </div>
            <Select value={current} onValueChange={(v) => v && changeLang(v)}>
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LANGUAGES.map(({ value, key }) => (
                  <SelectItem key={value} value={value}>
                    {t(key)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-center justify-between gap-4">
            <div>
              <p className="text-sm font-medium">{t("account.timezone")}</p>
              <p className="text-xs text-muted-foreground">
                {timezone === detectedTimezone
                  ? t("account.timezoneAutoDetected")
                  : t("account.timezoneManual")}
              </p>
            </div>
            <Select
              value={timezone}
              onValueChange={(v) => v && changeTimezone(v)}
            >
              <SelectTrigger className="w-56">
                <SelectValue />
              </SelectTrigger>
              {/* alignItemWithTrigger (the Select default) aligns the popup
                  so the selected item sits at the trigger's position — with
                  ~400 timezones, aligning one deep in the list pushes the
                  whole popup off toward the top of the viewport, detached
                  from the trigger. A plain anchored-below-trigger popup
                  works correctly for a list this long. */}
              <SelectContent className="max-h-72" alignItemWithTrigger={false}>
                {TIMEZONES.map((tz) => (
                  <SelectItem key={tz} value={tz}>
                    {tz.replace(/_/g, " ")}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
