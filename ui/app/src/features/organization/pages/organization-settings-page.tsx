import { Outlet, useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { usePermissions } from "@/hooks/use-permissions"
import { RouteTabs } from "@/components/shared/route-tabs"

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
  const { isOwner } = usePermissions()

  const base = `/organization/${organizationId}/settings`
  const TABS = [
    { label: t("organization.settings.tabs.general"), suffix: "" },
    { label: t("organization.settings.tabs.branding"), suffix: "/branding" },
    { label: t("organization.settings.tabs.security"), suffix: "/security" },
    ...(isOwner
      ? [{ label: t("organization.settings.tabs.danger"), suffix: "/danger" }]
      : []),
  ]

  return (
    <div className="max-w-2xl">
      <div className="mb-6">
        <RouteTabs base={base} tabs={TABS} />
      </div>
      <Outlet />
    </div>
  )
}
