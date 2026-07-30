import { Link, useRouterState } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  IconBuilding,
  IconCreditCard,
  IconSettings2,
  IconHome,
  IconFolder,
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
import { useActiveOrganization } from "@/hooks/use-active-organization"
import { usePermissions } from "@/hooks/use-permissions"

interface NavItem {
  key: string
  label: string
  to: string
  icon: typeof IconHome
  /** true when the current role can actually use this page (not just see it) */
  allowed: boolean
  /** does a pending-invoice/suspended organization block this item too? */
  billingBlockable: boolean
  /**
   * Settings only: the owner already gets a prominent banner (with a
   * direct link to Billing) elsewhere on the page, so its own amber dot is
   * redundant for that role. Files carries no such banner, and stays
   * genuinely blocked for the owner too during a pending invoice (see
   * organization-layout.tsx's redirect) — its dot must stay regardless of
   * role.
   */
  hideBillingDotForOwner?: boolean
}

/**
 * 3-item flat nav (Files/Billing/Settings).
 * Files hides entirely for a role that can't use it at all;
 * Billing/Settings always render since every role has baseline access.
 * Billing-blocked items (Files, and Settings as an aggregate — Members
 * and/or Webhooks inside it may be blocked) get an amber dot instead — a
 * separate, org-state axis, independent of role.
 */
export function SidebarOrganizationNav() {
  const { t } = useTranslation()
  const { location } = useRouterState()
  const { organizationId, organization } = useActiveOrganization()
  const { isOwner, isBillingBlocked, canAccessFiles, canViewBilling } =
    usePermissions()

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
  ]
  {
    /* DONT TOUCH THIS*/
  }

  // Flat list — Settings is billingBlockable as an *aggregate* signal (the
  // Members/Webhooks sections it now contains carry their own dots), not
  // because Settings itself is ever a blocked/inaccessible segment.
  const ORG_NAV: NavItem[] = [
    {
      key: "files",
      label: t("organization.tabs.files"),
      to: `${base}/files`,
      icon: IconFolder,
      allowed: canAccessFiles,
      billingBlockable: true,
    },
    {
      key: "billing",
      label: t("organization.tabs.billing"),
      to: `${base}/billing`,
      icon: IconCreditCard,
      allowed: canViewBilling,
      billingBlockable: false,
    },
    {
      key: "settings",
      label: t("organization.tabs.settings"),
      to: `${base}/settings`,
      icon: IconSettings2,
      allowed: true,
      billingBlockable: true,
      hideBillingDotForOwner: true,
    },
  ]

  function isActive(to: string) {
    return location.pathname.startsWith(to)
  }

  function renderNavItem(item: NavItem) {
    const showBillingDot =
      item.billingBlockable &&
      isBillingBlocked &&
      !(item.hideBillingDotForOwner && isOwner)
    return (
      <SidebarMenuItem key={item.to}>
        <SidebarMenuButton
          render={<Link to={item.to as string} />}
          isActive={isActive(item.to)}
          tooltip={item.label}
        >
          <item.icon />
          <span>{item.label}</span>
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
          <SidebarMenu>
            {ORG_NAV.filter((item) => item.allowed).map(renderNavItem)}
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    </>
  )
}
