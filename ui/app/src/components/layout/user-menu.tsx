import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  IconUser,
  IconLogout,
  IconSun,
  IconMoon,
  IconDeviceLaptop,
  IconLanguage,
} from "@tabler/icons-react"
import type { ComponentType, ReactNode } from "react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useAuth } from "@/components/auth-provider"
import { useTheme } from "@/components/theme-provider"
import { useTransientStore } from "@/hooks/use-transient"
import { useUpdatePreferences } from "@/features/account/hooks"
import i18n from "@/lib/i18n"

type Theme = "light" | "dark" | "system"

const THEME_ICON: Record<Theme, ComponentType<{ className?: string }>> = {
  light: IconSun,
  dark: IconMoon,
  system: IconDeviceLaptop,
}
const THEME_NEXT: Record<Theme, Theme> = {
  light: "dark",
  dark: "system",
  system: "light",
}

interface UserMenuProps {
  /** trigger content (avatar/name/chevron) — this component supplies the DropdownMenuTrigger itself */
  children: ReactNode
  triggerClassName?: string
  side?: "top" | "bottom"
  align?: "start" | "end"
  contentClassName?: string
}

/**
 * The account dropdown (avatar/name, theme cycle, language cycle, account
 * link, sign out) — was duplicated near-verbatim between SidebarUserMenu
 * (sidebar footer) and StandaloneNav (top bar). Callers only own the
 * trigger chrome; this owns the menu content and all its side effects.
 */
export function UserMenu({
  children,
  triggerClassName,
  side = "bottom",
  align = "end",
  contentClassName,
}: UserMenuProps) {
  const { t } = useTranslation()
  const { user } = useAuth()
  const { theme, setTheme } = useTheme()
  const { mutate: savePrefs } = useUpdatePreferences()

  const currentTheme = theme as Theme
  const ThemeIcon = THEME_ICON[currentTheme]

  function cycleTheme() {
    setTheme(THEME_NEXT[currentTheme])
  }
  function cycleLanguage() {
    const next = i18n.language === "en" ? "id" : "en"
    void i18n.changeLanguage(next)
    localStorage.setItem("lang", next)
    savePrefs({ lang: next })
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={triggerClassName}>
        {children}
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side={side}
        align={align}
        sideOffset={4}
        className={contentClassName}
      >
        <div className="truncate px-2 py-1 text-xs text-muted-foreground">
          {user?.email}
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem render={<Link to="/account" />}>
          <IconUser className="shrink-0" />
          {t("nav.account")}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={cycleTheme}>
          <ThemeIcon className="shrink-0" />
          {t(`theme.${currentTheme}`)}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={cycleLanguage}>
          <IconLanguage className="shrink-0" />
          {t(`language.${i18n.language === "en" ? "id" : "en"}`)}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          onClick={() => useTransientStore.setValue("signout-scope", "local")}
        >
          <IconLogout className="shrink-0" />
          {t("nav.signOut")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
