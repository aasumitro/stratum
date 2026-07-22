import { Link, useRouterState } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  IconBuilding,
  IconUser,
  IconBell,
  IconChevronLeft,
} from "@tabler/icons-react"
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { useOrganizations } from "@/features/organization/hooks"
import { useNotificationCount } from "@/features/notification/hooks"

/**
 * Personal-context sidebar body — exactly 3 items: Organizations,
 * Account, Notifications. This is the top-level swap counterpart to
 * SidebarOrganizationNav's 7 items — it must stay this short. The 6
 * account-related sections (Profile/Preferences/Security/Privacy &
 * data/Personal audit/API tokens) are page-local tabs *inside* the Account
 * page, not global sidebar entries — see AccountPage.
 */
export function SidebarPersonalNav() {
  const { t } = useTranslation()
  const { location } = useRouterState()
  const { data } = useOrganizations()
  const { data: countData } = useNotificationCount()
  const unread = countData?.data?.unread ?? 0
  // Plain synchronous browser API read — this is a client-only SPA (no SSR),
  // so reading it directly during render is safe and avoids an extra effect.
  const lastOrgId = localStorage.getItem("active_organization_id")

  const lastOrg = lastOrgId
    ? (data?.data ?? []).find((o) => o.id === lastOrgId)
    : undefined

  const NAV: {
    key: string
    label: string
    href: "/organizations" | "/account" | "/notifications"
    icon: typeof IconBuilding
    badge?: string
  }[] = [
    {
      key: "organizations",
      label: t("nav.organizations"),
      href: "/organizations",
      icon: IconBuilding,
    },
    {
      key: "account",
      label: t("nav.account"),
      href: "/account",
      icon: IconUser,
    },
    {
      key: "notifications",
      label: t("nav.notifications"),
      href: "/notifications",
      icon: IconBell,
      badge: unread > 0 ? (unread > 9 ? "9+" : String(unread)) : undefined,
    },
  ]

  function isActive(href: string) {
    return location.pathname === href
  }

  return (
    <SidebarGroup>
      <SidebarGroupContent>
        <SidebarMenu>
          {NAV.map((item) => (
            <SidebarMenuItem key={item.key}>
              <SidebarMenuButton
                render={<Link to={item.href} />}
                isActive={isActive(item.href)}
                tooltip={item.label}
              >
                <item.icon />
                <span className="flex-1">{item.label}</span>
                {item.badge && (
                  <span className="ml-auto flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold text-white group-data-[collapsible=icon]:hidden">
                    {item.badge}
                  </span>
                )}
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>

      {lastOrg && (
        <SidebarGroupContent className="mt-2 border-t pt-2">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                render={
                  <Link
                    to="/organization/$organizationId"
                    params={{ organizationId: lastOrg.id }}
                  />
                }
                tooltip={t("nav.backTo", { name: lastOrg.name })}
              >
                <IconChevronLeft />
                <span>{t("nav.backTo", { name: lastOrg.name })}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      )}
    </SidebarGroup>
  )
}
