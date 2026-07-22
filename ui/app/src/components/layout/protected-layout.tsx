import { useEffect, useRef } from "react"
import { Outlet } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar"
import { AppSidebar } from "@/components/layout/app-sidebar"
import { AppHeader } from "@/components/layout/app-header"
import { KeyboardShortcuts } from "@/components/shared/keyboard-shortcuts"
import { SignOutDialog } from "@/components/shared/sign-out-dialog"
import { CreateOrganizationPortal } from "@/components/layout/create-organization-portal"
import { JoinOrganizationPortal } from "@/components/layout/join-organization-portal"
import { useTheme } from "@/components/theme-provider"
import { useNotificationStream } from "@/features/notification/hooks"
import { useResponsiveSidebarOpen } from "@/hooks/use-responsive-sidebar"
import { useNavigationChords } from "@/hooks/use-navigation-chords"
import { queryKeys } from "@/lib/api/keys"
import type { UserProfile } from "@/types/account"
import type { HTTPResponse } from "@/lib/api/response"

// One shell everywhere: sidebar + top bar render on every authenticated
// page. Only onboarding stays outside this layout (full-screen, its own
// route tree). The sidebar body itself swaps between org-nav and
// personal-nav based on route — see AppSidebar.
export function ProtectedLayout() {
  const queryClient = useQueryClient()
  const { setTheme } = useTheme()
  const { i18n } = useTranslation()
  const prefApplied = useRef(false)

  // Hydrate lang/theme from DB preferences on first mount only.
  // Profile is already in cache from _protected.tsx beforeLoad — no extra fetch.
  // Guard prevents re-running when i18n triggers a re-render (languageChanged event),
  // which would read stale cache and revert a user-initiated language change.
  useEffect(() => {
    if (prefApplied.current) return
    prefApplied.current = true

    const cached = queryClient.getQueryData<HTTPResponse<UserProfile>>(
      queryKeys.account.me()
    )
    const prefs = cached?.data?.preferences
    if (!prefs) return

    const lang = prefs.lang as string | undefined
    const theme = prefs.theme as "light" | "dark" | "system" | undefined

    if (lang && ["en", "id"].includes(lang)) void i18n.changeLanguage(lang)
    if (theme && ["light", "dark", "system"].includes(theme)) setTheme(theme)
  }, [queryClient, setTheme, i18n])

  useNotificationStream()
  useNavigationChords()
  const [sidebarOpen, setSidebarOpen] = useResponsiveSidebarOpen()

  return (
    <SidebarProvider open={sidebarOpen} onOpenChange={setSidebarOpen}>
      <AppSidebar />
      <SidebarInset>
        <AppHeader />
        <main className="flex flex-1 flex-col p-4 md:p-6">
          <Outlet />
        </main>
      </SidebarInset>
      <KeyboardShortcuts />
      <SignOutDialog />
      <CreateOrganizationPortal />
      <JoinOrganizationPortal />
    </SidebarProvider>
  )
}
