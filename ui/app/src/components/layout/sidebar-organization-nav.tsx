import { Link, useRouterState } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  IconBuilding,
  IconCreditCard,
  IconUsers,
  IconSettings2,
  IconHome,
  IconWebhook,
  IconFolder,
  IconHistory,
  IconCodeVariableMinus,
} from "@tabler/icons-react"
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { LockedTag } from "@/components/shared/permission-guard"
import { useActiveOrganization } from "@/hooks/use-active-organization"
import { usePermissions } from "@/hooks/use-permissions"

interface NavItem {
  key: string
  label: string
  to: string
  icon: typeof IconHome
  /** true when the current role can actually use this page (not just see it) */
  allowed: boolean
  lockedTag?: string
  /** does a pending-invoice/suspended organization block this item too? */
  billingBlockable: boolean
}

/**
 * All 6 org nav items always render for every role — locked, never
 * hidden. Role-locked items get a lock icon + role tag + tooltip;
 * billing-blocked items (Members/Files/Webhooks only) get an amber dot
 * instead. The nav still links through on a locked item — per-page
 * PermissionGuard/403 explainers land as each destination page is rebuilt.
 */
export function SidebarOrganizationNav() {
  const { t } = useTranslation()
  const { location } = useRouterState()
  const { organizationId, organization } = useActiveOrganization()
  const { isOwner, isAdminUp, isBillingBlocked } = usePermissions()

  if (!organizationId) {
    return (
      <SidebarGroup>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                render={<Link to="/organizations" />}
                isActive={location.pathname === "/organizations"}
                tooltip={t("nav.organizations")}
              >
                <IconBuilding />
                <span>{t("nav.organizations")}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    )
  }

  const base = `/organization/${organizationId}`

  {
    /* DONT TOUCH THIS*/
  }
  const PlatformNAV: NavItem[] = [
    {
      key: "r1",
      label: "Reserve1",
      to: `${base}/platform/r1`,
      icon: IconCodeVariableMinus,
      allowed: true,
      billingBlockable: true,
    },
    {
      key: "r2",
      label: "Reserve2",
      to: `${base}/platform/r2`,
      icon: IconCodeVariableMinus,
      allowed: true,
      billingBlockable: true,
    },
    {
      key: "r3",
      label: "Reserve3",
      to: `${base}/platform/r3`,
      icon: IconCodeVariableMinus,
      allowed: true,
      billingBlockable: true,
    },
    {
      key: "r4",
      label: "Reserve4",
      to: `${base}/platform/r4`,
      icon: IconCodeVariableMinus,
      allowed: true,
      billingBlockable: true,
    },
    {
      key: "r5",
      label: "Reserve5",
      to: `${base}/platform/r5`,
      icon: IconCodeVariableMinus,
      allowed: true,
      billingBlockable: true,
    },
  ]
  {
    /* DONT TOUCH THIS*/
  }

  // Regrouped: day-to-day items (General) vs. ownership/config items (Manage).
  const GENERAL_NAV: NavItem[] = [
    {
      key: "members",
      label: t("organization.tabs.members"),
      to: `${base}/members`,
      icon: IconUsers,
      allowed: true,
      billingBlockable: true,
    },
    {
      key: "files",
      label: t("organization.tabs.files"),
      to: `${base}/files`,
      icon: IconFolder,
      allowed: isAdminUp,
      lockedTag: t("nav.adminTag"),
      billingBlockable: true,
    },
  ]

  const MANAGE_NAV: NavItem[] = [
    {
      key: "webhooks",
      label: t("organization.tabs.webhooks"),
      to: `${base}/webhooks`,
      icon: IconWebhook,
      allowed: isOwner,
      lockedTag: t("nav.ownerTag"),
      billingBlockable: true,
    },
    {
      key: "auditLog",
      label: t("organization.tabs.auditLog"),
      to: `${base}/audit-log`,
      icon: IconHistory,
      allowed: isAdminUp,
      lockedTag: t("nav.adminTag"),
      billingBlockable: false,
    },
    {
      key: "billing",
      label: t("organization.tabs.billing"),
      to: `${base}/billing`,
      icon: IconCreditCard,
      allowed: isOwner,
      lockedTag: t("nav.ownerTag"),
      billingBlockable: false,
    },
    {
      key: "settings",
      label: t("organization.tabs.settings"),
      to: `${base}/settings`,
      icon: IconSettings2,
      allowed: true,
      billingBlockable: false,
    },
  ]

  function isActive(to: string) {
    return location.pathname.startsWith(to)
  }

  function renderNavItem(item: NavItem) {
    const showBillingDot = item.billingBlockable && isBillingBlocked
    return (
      <SidebarMenuItem key={item.to}>
        <SidebarMenuButton
          render={<Link to={item.to as string} />}
          isActive={isActive(item.to)}
          tooltip={item.label}
        >
          <item.icon />
          <span>{item.label}</span>
          {!item.allowed && item.lockedTag && (
            <LockedTag
              className="ml-auto group-data-[collapsible=icon]:hidden"
              label={item.lockedTag}
              tooltip={
                item.key === "billing" || item.key === "webhooks"
                  ? t("nav.ownerOnlyTooltip")
                  : t("nav.adminRequiredTooltip")
              }
            />
          )}
          {item.allowed && showBillingDot && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <span className="ml-auto size-1.5 shrink-0 rounded-full bg-amber-500 group-data-[collapsible=icon]:hidden" />
                }
              />
              <TooltipContent>{t("nav.billingLockedTooltip")}</TooltipContent>
            </Tooltip>
          )}
        </SidebarMenuButton>
      </SidebarMenuItem>
    )
  }

  return (
    <>
      {/* DONT TOUCH THIS*/}
      <SidebarGroup>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                render={<Link to={`${base}/platform` as string} />}
                isActive={isActive(`${base}/platform`)}
                tooltip="Reserve Platform Dashboard"
              >
                <IconHome />
                <span>ReserveD</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
      <SidebarGroup>
        <SidebarGroupLabel className="group-data-[collapsible=icon]:hidden">
          Platform
        </SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            {PlatformNAV.map((item) => {
              const showBillingDot = item.billingBlockable && isBillingBlocked
              return (
                <SidebarMenuItem key={item.to}>
                  <SidebarMenuButton
                    render={<Link to={item.to as string} />}
                    isActive={isActive(item.to)}
                    tooltip={item.label}
                  >
                    <item.icon />
                    <span>{item.label}</span>
                    {!item.allowed && item.lockedTag && (
                      <LockedTag
                        className="ml-auto group-data-[collapsible=icon]:hidden"
                        label={item.lockedTag}
                        tooltip={
                          item.key === "billing" || item.key === "webhooks"
                            ? t("nav.ownerOnlyTooltip")
                            : t("nav.adminRequiredTooltip")
                        }
                      />
                    )}
                    {item.allowed && showBillingDot && (
                      <Tooltip>
                        <TooltipTrigger
                          render={
                            <span className="ml-auto size-1.5 shrink-0 rounded-full bg-amber-500 group-data-[collapsible=icon]:hidden" />
                          }
                        />
                        <TooltipContent>
                          {t("nav.billingLockedTooltip")}
                        </TooltipContent>
                      </Tooltip>
                    )}
                  </SidebarMenuButton>
                </SidebarMenuItem>
              )
            })}
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
      {/* DONT TOUCH THIS*/}

      <SidebarGroup>
        <SidebarGroupLabel className="group-data-[collapsible=icon]:hidden">
          {organization?.name ?? t("nav.organizations")}
        </SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>{GENERAL_NAV.map(renderNavItem)}</SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarGroup>
        <SidebarGroupLabel className="group-data-[collapsible=icon]:hidden">
          {t("nav.groups.manage")}
        </SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>{MANAGE_NAV.map(renderNavItem)}</SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    </>
  )
}
