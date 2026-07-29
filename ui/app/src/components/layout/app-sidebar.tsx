import { useTranslation } from "react-i18next"
import { Link } from "@tanstack/react-router"
import { IconBuilding, IconSelector } from "@tabler/icons-react"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
} from "@/components/ui/sidebar"
import { SidebarOrganizationNav } from "@/components/layout/sidebar-organization-nav"
import { SidebarPersonalNav } from "@/components/layout/sidebar-personal-nav"
import { SidebarUserMenu } from "@/components/layout/sidebar-user-menu"
import { OrganizationSwitcher } from "@/components/layout/organization-switcher"
import { SidebarSetupCards } from "@/features/organization/components/sidebar-setup-cards"
import { useActiveOrganization } from "@/hooks/use-active-organization"
import { useOrganizations } from "@/features/organization/hooks/use-organization"
import { useProfile } from "@/features/account/hooks"

const STRATA_MINI = [
  { width: "72%", delay: "0s" },
  { width: "48%", delay: "0.7s" },
  { width: "88%", delay: "1.3s" },
]

export function AppSidebar() {
  const { t } = useTranslation()
  const { organizationId } = useActiveOrganization()
  const { data } = useOrganizations()
  const { data: profileData } = useProfile()
  const organizations = data?.data ?? []
  const defaultOrganizationId = profileData?.data?.preferences
    ?.default_organization_id as string | undefined

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        {organizationId ? (
          // Static branding once inside an org — the breadcrumb instance
          // (app-header.tsx) is the sole functional switcher here.
          <Link
            to="/organization/$organizationId/settings"
            params={{ organizationId }}
            className="flex w-full items-center gap-2 rounded-xl px-2.5 py-2 text-sm font-semibold text-sidebar-foreground group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-2"
          >
            <span className="flex size-5 shrink-0 items-center justify-center rounded-md border">
              <IconBuilding className="size-3" />
            </span>
            <span className="min-w-0 flex-1 truncate text-left group-data-[collapsible=icon]:hidden">
              Stratum
            </span>
          </Link>
        ) : (
          <OrganizationSwitcher
            organizations={organizations}
            activeOrganizationId={organizationId}
            defaultOrganizationId={defaultOrganizationId}
            enableShortcut
            triggerClassName="flex w-full items-center gap-2 rounded-xl border px-2.5 py-2 text-sm font-semibold text-sidebar-foreground transition-colors hover:bg-sidebar-accent group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-2"
          >
            <span className="flex size-5 shrink-0 items-center justify-center rounded-md border">
              <IconBuilding className="size-3" />
            </span>
            <span className="min-w-0 flex-1 truncate text-left group-data-[collapsible=icon]:hidden">
              {t("nav.myAccount")}
            </span>
            <IconSelector className="size-3.5 shrink-0 text-sidebar-foreground/50 group-data-[collapsible=icon]:hidden" />
          </OrganizationSwitcher>
        )}
      </SidebarHeader>

      <SidebarContent>
        {organizationId ? <SidebarOrganizationNav /> : <SidebarPersonalNav />}
      </SidebarContent>

      <SidebarFooter>
        {organizationId && (
          <SidebarSetupCards organizationId={organizationId} />
        )}
        <SidebarUserMenu />
        <div
          className="pointer-events-none flex flex-col gap-1 px-3 pb-2 group-data-[collapsible=icon]:hidden"
          aria-hidden
        >
          {STRATA_MINI.map((s, i) => (
            <div
              key={i}
              className="h-0.5 animate-strata rounded-full bg-sidebar-foreground/15"
              style={{ width: s.width, animationDelay: s.delay }}
            />
          ))}
        </div>
      </SidebarFooter>
    </Sidebar>
  )
}
