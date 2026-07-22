import { useState } from "react"
import { Outlet, useParams, Link, useRouterState } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconUpload, IconMail } from "@tabler/icons-react"
import type { OrganizationRole } from "@/types/organization"
import {
  useOrganizations,
  useOrganizationMembers,
  useOrganizationInvitations,
} from "@/features/organization/hooks"
import { ImportMembersSheet } from "@/features/organization/components/import-members-sheet"
import { InviteEmailSheet } from "@/features/organization/components/invite-email-sheet"
import { PageHeader } from "@/components/shared/page-header"
import { Button } from "@/components/ui/button"
import { LockedTag } from "@/components/shared/permission-guard"
import { cn } from "@/lib/ui"

// Members/Invitations as real routes (was client `useState` tab),
// mirroring the pattern Billing already validated: deep-linkable,
// shareable, back-button-correct.
export function MembersPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { location } = useRouterState()
  const [importOpen, setImportOpen] = useState(false)
  const [inviteOpen, setInviteOpen] = useState(false)

  const { data: listData } = useOrganizations()
  const role = listData?.data?.find((w) => w.id === organizationId)?.role as
    | OrganizationRole
    | undefined
  const canManage = role === "owner" || role === "admin"
  const isOwner = role === "owner"

  const { data: membersData } = useOrganizationMembers(organizationId)
  const { data: invitationsData } = useOrganizationInvitations(
    organizationId,
    canManage
  )
  const memberCount = membersData?.data?.length ?? 0
  const inviteCount = invitationsData?.data?.length ?? 0

  const base = `/organization/${organizationId}/members`
  const TABS = [
    {
      id: "members",
      label: t("organization.members.tabLabel", { count: memberCount }),
      suffix: "",
    },
    {
      id: "invitations",
      label: t("organization.invitations.tabLabel", { count: inviteCount }),
      suffix: "/invitations",
    },
  ] as const

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t("organization.tabs.members")}
        actions={
          canManage && (
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setImportOpen(true)}
              >
                <IconUpload data-icon="inline-start" />
                {t("organization.members.importMembers")}
              </Button>
              <Button size="sm" onClick={() => setInviteOpen(true)}>
                <IconMail data-icon="inline-start" />
                {t("organization.members.inviteMember")}
              </Button>
            </div>
          )
        }
      />

      <ImportMembersSheet
        organizationId={organizationId}
        open={importOpen}
        onOpenChange={setImportOpen}
      />
      <InviteEmailSheet
        organizationId={organizationId}
        open={inviteOpen}
        onOpenChange={setInviteOpen}
        isOwner={isOwner}
      />

      <div className="flex gap-1 border-b">
        {TABS.map((tab) => {
          const href = `${base}${tab.suffix}`
          const isActive =
            tab.suffix === ""
              ? location.pathname === base || location.pathname === `${base}/`
              : location.pathname === href
          return (
            <Link
              key={tab.id}
              to={href as string}
              className={cn(
                "-mb-px flex items-center gap-2 border-b-2 px-4 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              )}
            >
              {tab.label}
              {tab.id === "invitations" && !canManage && (
                <LockedTag
                  label={t("nav.adminTag")}
                  tooltip={t("nav.adminRequiredTooltip")}
                />
              )}
            </Link>
          )
        })}
      </div>

      <Outlet />
    </div>
  )
}
