import { useState } from "react"
import { Outlet, useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconUpload, IconMail, IconChevronDown } from "@tabler/icons-react"
import {
  useOrganizationMembers,
  useOrganizationInvitations,
} from "@/features/organization/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { ImportMembersSheet } from "@/features/organization/components/import-members-sheet"
import { InviteEmailSheet } from "@/features/organization/components/invite-email-sheet"
import { PageHeader } from "@/components/shared/page-header"
import { RouteTabs } from "@/components/shared/route-tabs"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

// Members/Invitations as real routes,
// mirroring the pattern Billing already validated: deep-linkable,
// shareable, back-button-correct.
export function MembersPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const [importOpen, setImportOpen] = useState(false)
  const [inviteOpen, setInviteOpen] = useState(false)

  const { canManageMembers: canManage, isOwner } = usePermissions()

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
      label: t("organization.members.tabLabel", { count: memberCount }),
      suffix: "",
    },
    ...(canManage
      ? [
          {
            label: t("organization.invitations.tabLabel", {
              count: inviteCount,
            }),
            suffix: "/invitations",
          },
        ]
      : []),
  ]

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t("organization.tabs.members")}
        actions={
          canManage && (
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button size="sm" />}>
                {t("organization.members.addMembers")}
                <IconChevronDown data-icon="inline-end" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => setInviteOpen(true)}>
                  <IconMail className="shrink-0" />
                  {t("organization.members.inviteMember")}
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => setImportOpen(true)}>
                  <IconUpload className="shrink-0" />
                  {t("organization.members.importMembers")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
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

      <RouteTabs base={base} tabs={TABS} />

      <Outlet />
    </div>
  )
}
