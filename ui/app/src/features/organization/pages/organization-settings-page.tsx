import { Outlet, useParams, Link, useRouterState } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { cn } from "@/lib/ui"

// 4 tabs, now real routes (was client `Tabs` state) so any tab is
// deep-linkable/shareable/back-button-safe, mirroring the pattern Billing
// already uses: General (name/slug/logo/timezone/locale) · Branding
// (placeholder) · Security (IP allowlist — split out from the old "Locale"
// tab, which bundled a timezone form and a network-access-control feature
// under one label a security-minded admin wouldn't think to look under) ·
// Danger zone (owner only, 403-explained for everyone else).
export function OrganizationSettingsPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { location } = useRouterState()

  const base = `/organization/${organizationId}/settings`
  const TABS = [
    { label: t("organization.settings.tabs.general"), suffix: "" },
    { label: t("organization.settings.tabs.branding"), suffix: "/branding" },
    { label: t("organization.settings.tabs.security"), suffix: "/security" },
    { label: t("organization.settings.tabs.danger"), suffix: "/danger" },
  ] as const

  return (
    <div className="max-w-2xl">
      <nav className="mb-6 flex flex-wrap gap-1">
        {TABS.map((tab) => {
          const href = `${base}${tab.suffix}`
          const isActive =
            tab.suffix === ""
              ? location.pathname === base || location.pathname === `${base}/`
              : location.pathname === href
          return (
            <Link
              key={tab.suffix || "general"}
              to={href as string}
              className={cn(
                "rounded-lg px-3 py-1.5 text-sm font-medium transition-colors",
                isActive
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground"
              )}
            >
              {tab.label}
            </Link>
          )
        })}
      </nav>
      <Outlet />
    </div>
  )
}
