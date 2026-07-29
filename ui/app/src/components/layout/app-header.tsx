import { Fragment } from "react"
import { useRouterState, useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconBuilding, IconSelector } from "@tabler/icons-react"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Separator } from "@/components/ui/separator"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { NotificationBell } from "@/components/shared/notification-bell"
import { OrganizationSwitcher } from "@/components/layout/organization-switcher"
import { useOrganizations } from "@/features/organization/hooks/use-organization"

export function AppHeader() {
  const { t } = useTranslation()
  const { location } = useRouterState()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId?: string
  }

  const { data: wsData } = useOrganizations()
  const organizations = wsData?.data ?? []
  const activeOrganization = organizationId
    ? organizations.find((w) => w.id === organizationId)
    : null

  const segments = location.pathname.split("/").filter(Boolean)
  const isOrganizationRoute = segments[0] === "organization" && !!organizationId
  const subSegments = isOrganizationRoute ? segments.slice(2) : segments

  function label(segment: string): string {
    const key = `breadcrumb.${segment}`
    const translated = t(key)
    return translated !== key
      ? translated
      : segment.charAt(0).toUpperCase() + segment.slice(1)
  }

  return (
    <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mr-2 h-12" />

      <Breadcrumb>
        <BreadcrumbList>
          {isOrganizationRoute ? (
            <>
              <BreadcrumbItem>
                <OrganizationSwitcher
                  organizations={organizations}
                  activeOrganizationId={organizationId}
                  enableShortcut={isOrganizationRoute}
                  triggerClassName="flex items-center gap-1 rounded px-1 py-0.5 text-sm font-medium transition-colors hover:bg-accent hover:text-foreground focus:outline-none"
                >
                  <IconBuilding className="size-3.5 shrink-0" />
                  <span className="max-w-36 truncate">
                    {activeOrganization?.name ?? t("nav.selectOrganization")}
                  </span>
                  <IconSelector className="size-3 shrink-0 text-muted-foreground" />
                </OrganizationSwitcher>
              </BreadcrumbItem>
              {subSegments.map((seg, i) => {
                const isLast = i === subSegments.length - 1
                return (
                  <Fragment key={seg}>
                    <BreadcrumbSeparator />
                    <BreadcrumbItem>
                      {isLast ? (
                        <BreadcrumbPage>{label(seg)}</BreadcrumbPage>
                      ) : (
                        <a
                          href={`/organization/${organizationId}/${subSegments.slice(0, i + 1).join("/")}`}
                          className="text-sm text-muted-foreground transition-colors hover:text-foreground"
                        >
                          {label(seg)}
                        </a>
                      )}
                    </BreadcrumbItem>
                  </Fragment>
                )
              })}
            </>
          ) : (
            segments.map((seg, i) => {
              const isLast = i === segments.length - 1
              return (
                <Fragment key={seg}>
                  <BreadcrumbItem>
                    {isLast ? (
                      <BreadcrumbPage>{label(seg)}</BreadcrumbPage>
                    ) : (
                      <a
                        href={`/${segments.slice(0, i + 1).join("/")}`}
                        className="text-sm text-muted-foreground transition-colors hover:text-foreground"
                      >
                        {label(seg)}
                      </a>
                    )}
                  </BreadcrumbItem>
                  {!isLast && <BreadcrumbSeparator />}
                </Fragment>
              )
            })
          )}
        </BreadcrumbList>
      </Breadcrumb>

      <div className="ml-auto">
        <NotificationBell organizationId={organizationId} />
      </div>
    </header>
  )
}
