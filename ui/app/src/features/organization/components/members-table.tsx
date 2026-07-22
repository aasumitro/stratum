import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconDoorExit, IconDots, IconCopy } from "@tabler/icons-react"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Button } from "@/components/ui/button"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"
import { SearchBar } from "@/components/shared/search-bar"
import {
  useOrganizationMembers,
  useChangeMemberRole,
  useRemoveMember,
  useLeaveOrganization,
} from "@/features/organization/hooks"
import { useAuth } from "@/components/auth-provider"
import { initials } from "@/lib/format"
import type { OrganizationRole } from "@/types/organization"

const ROLE_BADGE: Record<OrganizationRole, string> = {
  owner: "bg-primary/10 text-primary",
  admin: "bg-blue-500/10 text-blue-600",
  member: "bg-muted text-muted-foreground",
}

interface Props {
  organizationId: string
  currentRole?: OrganizationRole
}

export function MembersTable({ organizationId, currentRole }: Props) {
  const { t } = useTranslation()
  const { user } = useAuth()
  const navigate = useNavigate()
  const [search, setSearch] = useState("")
  const [roleFilter, setRoleFilter] = useState<string>("all")
  const [removeTarget, setRemoveTarget] = useState<{
    authSub: string
    name: string
  } | null>(null)

  const { data, isLoading } = useOrganizationMembers(organizationId)
  const { mutate: changeRole } = useChangeMemberRole(organizationId)
  const { mutate: removeMember, isPending: removing } =
    useRemoveMember(organizationId)
  const { mutate: leaveOrganization, isPending: leaving } =
    useLeaveOrganization(organizationId)

  const allMembers = data?.data ?? []
  const filtered = allMembers.filter((m) => {
    if (roleFilter !== "all" && m.role !== roleFilter) return false
    if (!search) return true
    const q = search.toLowerCase()
    return (
      (m.full_name ?? "").toLowerCase().includes(q) ||
      (m.email ?? "").toLowerCase().includes(q)
    )
  })
  const canManage = currentRole === "owner" || currentRole === "admin"

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <SearchBar
          value={search}
          onChange={setSearch}
          placeholder={t("organization.members.searchPlaceholder")}
          className="w-64"
        />
        <Select
          value={roleFilter}
          onValueChange={(v) => setRoleFilter(v ?? "all")}
        >
          <SelectTrigger className="h-9 w-32 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">
              {t("organization.members.roleAll")}
            </SelectItem>
            <SelectItem value="owner">
              {t("organization.roles.owner")}
            </SelectItem>
            <SelectItem value="admin">
              {t("organization.roles.admin")}
            </SelectItem>
            <SelectItem value="member">
              {t("organization.roles.member")}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="rounded-xl border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("organization.members.memberCol")}</TableHead>
              <TableHead>{t("organization.members.emailCol")}</TableHead>
              <TableHead>{t("organization.members.roleCol")}</TableHead>
              <TableHead>{t("organization.members.joinedCol")}</TableHead>
              <TableHead className="hidden w-10 md:table-cell" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading
              ? Array.from({ length: 3 }).map((_, i) => (
                  <TableRow key={i}>
                    {Array.from({ length: 5 }).map((__, j) => (
                      <TableCell key={j}>
                        <div className="h-4 w-24 animate-pulse rounded bg-muted" />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              : filtered.map((m) => {
                  const isSelf = m.auth_sub === user?.id
                  const isThisOwner = m.role === "owner"
                  const memberInitials = initials(m.full_name || m.email || "?")
                  const name = m.full_name ?? m.auth_sub

                  return (
                    <TableRow key={m.id}>
                      <TableCell>
                        <div className="flex items-center gap-3">
                          <Avatar className="size-7 shrink-0 rounded-lg">
                            <AvatarImage src={m.avatar_url} alt="" />
                            <AvatarFallback className="rounded-lg text-xs">
                              {memberInitials}
                            </AvatarFallback>
                          </Avatar>
                          <span className="flex items-center gap-1.5 text-sm font-medium">
                            {name}
                            {isSelf && (
                              <Badge variant="outline" className="text-[10px]">
                                {t("organization.members.you")}
                              </Badge>
                            )}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {m.email ?? "—"}
                      </TableCell>
                      <TableCell>
                        {canManage && !isThisOwner ? (
                          <Select
                            value={m.role}
                            onValueChange={(v) =>
                              changeRole({
                                authSub: m.auth_sub,
                                role: v as OrganizationRole,
                              })
                            }
                          >
                            <SelectTrigger className="h-7 w-24 text-xs">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="admin">
                                {t("organization.roles.admin")}
                              </SelectItem>
                              <SelectItem value="member">
                                {t("organization.roles.member")}
                              </SelectItem>
                            </SelectContent>
                          </Select>
                        ) : (
                          <span
                            className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize ${ROLE_BADGE[m.role]}`}
                          >
                            {m.role}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {new Date(m.joined_at).toLocaleDateString()}
                      </TableCell>
                      <TableCell className="hidden md:table-cell">
                        {canManage && !isThisOwner && !isSelf && (
                          <DropdownMenu>
                            <DropdownMenuTrigger
                              render={
                                <Button
                                  variant="ghost"
                                  size="icon-sm"
                                  aria-label={t("organization.members.actions")}
                                />
                              }
                            >
                              <IconDots className="size-4" />
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                              <DropdownMenuItem
                                onClick={() => {
                                  if (m.email) {
                                    void navigator.clipboard.writeText(m.email)
                                    toast.success(
                                      t("organization.members.emailCopied")
                                    )
                                  }
                                }}
                              >
                                <IconCopy className="shrink-0" />
                                {t("organization.members.copyEmail")}
                              </DropdownMenuItem>
                              <DropdownMenuItem
                                variant="destructive"
                                onClick={() =>
                                  setRemoveTarget({ authSub: m.auth_sub, name })
                                }
                              >
                                {t("organization.members.remove")}
                              </DropdownMenuItem>
                            </DropdownMenuContent>
                          </DropdownMenu>
                        )}
                        {!canManage && isSelf && !isThisOwner && (
                          <ConfirmationDialog
                            render={
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={leaving}
                              />
                            }
                            title={t("organization.members.leaveTitle")}
                            description={t(
                              "organization.members.leaveDescription"
                            )}
                            confirmLabel={t("organization.members.leave")}
                            pending={leaving}
                            onConfirm={() =>
                              leaveOrganization(undefined, {
                                onSuccess: () =>
                                  void navigate({ to: "/organizations" }),
                              })
                            }
                          >
                            <IconDoorExit data-icon="inline-start" />
                            {t("organization.members.leave")}
                          </ConfirmationDialog>
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })}
          </TableBody>
        </Table>
      </div>

      {removeTarget && (
        <ConfirmationDialog
          open
          onOpenChange={(open) => !open && setRemoveTarget(null)}
          render={<span className="hidden" />}
          nativeButton={false}
          title={t("organization.members.removeTitle")}
          description={t("organization.members.removeDescription", {
            name: removeTarget.name,
          })}
          consequences={[t("organization.members.removeConsequence")]}
          confirmLabel={t("organization.members.remove")}
          pending={removing}
          onConfirm={() =>
            removeMember(removeTarget.authSub, {
              onSuccess: () => setRemoveTarget(null),
            })
          }
        >
          {null}
        </ConfirmationDialog>
      )}
    </div>
  )
}
