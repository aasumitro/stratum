import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  IconDoorExit,
  IconCopy,
  IconMail,
  IconUsers,
} from "@tabler/icons-react"
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
import { Button } from "@/components/ui/button"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { toastWithUndo } from "@/components/shared/toast-with-undo"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"
import { EmptyState } from "@/components/shared/empty-state"
import { SearchBar } from "@/components/shared/search-bar"
import { DataTablePagination } from "@/components/shared/pagination"
import { StatusBadge } from "@/components/shared/status-badge"
import {
  useOrganizationMembers,
  useChangeMemberRole,
  useRemoveMember,
  useSuspendMember,
  useReinstateMember,
  useLeaveOrganization,
} from "@/features/organization/hooks/use-members"
import {
  useOrganizationInvitations,
  useRevokeInvitation,
  useResendInvitation,
  useInvite,
} from "@/features/organization/hooks/use-invitations"
import { useAuth } from "@/components/auth-provider"
import { initials } from "@/lib/format"
import type { OrganizationRole, Member, Invitation } from "@/types/organization"

const PAGE_SIZE = 10

const ROLE_BADGE: Record<OrganizationRole, string> = {
  owner: "bg-primary/10 text-primary",
  admin: "bg-muted text-muted-foreground",
  member: "bg-muted text-muted-foreground",
}

type Row =
  | { kind: "member"; key: string; data: Member }
  | { kind: "invitation"; key: string; data: Invitation }

function isExpired(iso: string): boolean {
  return new Date(iso).getTime() < Date.now()
}

function maskEmail(email: string): string {
  const parts = email.split("@")
  if (parts.length !== 2) return email
  const [user, domain] = parts
  if (user.length <= 2) return `${user}***@${domain}`
  if (user.length <= 4)
    return `${user.slice(0, 1)}***${user.slice(-1)}@${domain}`
  return `${user.slice(0, 2)}***${user.slice(-2)}@${domain}`
}

interface Props {
  organizationId: string
  currentRole?: OrganizationRole
}

/**
 * One list for both active members and pending invitations — pending rows
 * are visually distinguished (muted avatar, "Pending" tag, resend/revoke
 * actions) rather than living in a second table/disclosure elsewhere on the
 * page. A real bordered table on desktop; a compact contact-card list on
 * mobile (not the generic stacked label/value card every `DataTable`
 * consumer gets — a member row is simple enough to read at a glance without
 * repeating "Email:"/"Role:"/"Joined:" labels for every entry). Pending rows
 * only load for admins+, since the invitations-list endpoint is admin-gated
 * server-side.
 */
export function MembersTable({ organizationId, currentRole }: Props) {
  const { t } = useTranslation()
  const { user } = useAuth()
  const navigate = useNavigate()
  const [search, setSearch] = useState("")
  const [roleFilter, setRoleFilter] = useState<string>("all")
  const [page, setPage] = useState(1)
  const [removeTarget, setRemoveTarget] = useState<{
    authSub: string
    name: string
  } | null>(null)
  const [suspendTarget, setSuspendTarget] = useState<{
    authSub: string
    name: string
  } | null>(null)
  const [reinstateTarget, setReinstateTarget] = useState<{
    authSub: string
    name: string
  } | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<Invitation | null>(null)
  const [resendTarget, setResendTarget] = useState<Invitation | null>(null)

  const canManage = currentRole === "owner" || currentRole === "admin"

  const { data, isLoading } = useOrganizationMembers(organizationId)
  const { data: invitationsData, isLoading: invitationsLoading } =
    useOrganizationInvitations(organizationId, canManage)
  const { mutate: changeRole } = useChangeMemberRole(organizationId)
  const { mutate: removeMember, isPending: removing } =
    useRemoveMember(organizationId)
  const { mutate: suspendMember, isPending: suspending } =
    useSuspendMember(organizationId)
  const { mutate: reinstateMember, isPending: reinstating } =
    useReinstateMember(organizationId)
  const { mutate: leaveOrganization, isPending: leaving } =
    useLeaveOrganization(organizationId)
  const { mutate: revoke, isPending: revoking } =
    useRevokeInvitation(organizationId)
  const { mutate: resend, isPending: resending } =
    useResendInvitation(organizationId)
  const { mutate: reinvite } = useInvite(organizationId)

  const allMembers = data?.data ?? []
  const allInvitations = invitationsData?.data ?? []
  const roleWeight: Record<OrganizationRole, number> = {
    owner: 1,
    admin: 2,
    member: 3,
  }

  const rows: Row[] = [
    ...allMembers.map((m): Row => ({ kind: "member", key: m.id, data: m })),
    ...allInvitations.map((inv): Row => ({
      kind: "invitation",
      key: inv.id,
      data: inv,
    })),
  ].sort((a, b) => {
    // 1. Members before invitations
    if (a.kind === "member" && b.kind === "invitation") return -1
    if (a.kind === "invitation" && b.kind === "member") return 1

    // 2. Sort by role
    const wA = roleWeight[a.data.role] ?? 99
    const wB = roleWeight[b.data.role] ?? 99
    if (wA !== wB) return wA - wB

    // 3. Alphabetical fallback
    const emailA = (a.data.email ?? "").toLowerCase()
    const emailB = (b.data.email ?? "").toLowerCase()
    return emailA.localeCompare(emailB)
  })

  const q = search.toLowerCase()
  const filtered = rows.filter((row) => {
    if (roleFilter !== "all" && row.data.role !== roleFilter) return false
    if (!q) return true
    if (row.kind === "member") {
      return (
        (row.data.full_name ?? "").toLowerCase().includes(q) ||
        (row.data.email ?? "").toLowerCase().includes(q)
      )
    }
    return row.data.email.toLowerCase().includes(q)
  })
  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))

  // Reset to page 1 whenever the filters change — adjusting state during
  // render (React's recommended pattern for this) rather than an effect.
  const [prevFilters, setPrevFilters] = useState({ search, roleFilter })
  if (prevFilters.search !== search || prevFilters.roleFilter !== roleFilter) {
    setPrevFilters({ search, roleFilter })
    setPage(1)
  }

  const paged = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)

  function memberName(m: Member) {
    return m.full_name ?? m.auth_sub
  }

  function RoleControl({ row }: { row: Row }) {
    if (row.kind === "invitation") {
      return (
        <span
          className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize ${ROLE_BADGE[row.data.role]}`}
        >
          {row.data.role}
        </span>
      )
    }
    const m = row.data
    const isThisOwner = m.role === "owner"
    if (canManage && !isThisOwner) {
      return (
        <Select
          value={m.role}
          onValueChange={(v) =>
            changeRole({ authSub: m.auth_sub, role: v as OrganizationRole })
          }
        >
          <SelectTrigger className="h-5 w-[90px] text-[11px] capitalize shadow-none">
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
      )
    }
    return (
      <span
        className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize ${ROLE_BADGE[m.role]}`}
      >
        {m.role}
      </span>
    )
  }

  function JoinedOrExpiry({ row }: { row: Row }) {
    if (row.kind === "member") {
      return new Date(row.data.joined_at).toLocaleDateString()
    }
    return isExpired(row.data.expires_at) ? (
      <StatusBadge
        status="expired"
        label={t("organization.invitations.expired")}
      />
    ) : (
      new Date(row.data.expires_at).toLocaleDateString()
    )
  }

  function Actions({ row }: { row: Row }) {
    if (row.kind === "invitation") {
      if (!canManage) return null
      return (
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            disabled={resending}
            onClick={() => setResendTarget(row.data)}
          >
            {t("organization.invitations.resend")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive"
            onClick={() => setRevokeTarget(row.data)}
          >
            {t("organization.invitations.revoke")}
          </Button>
        </div>
      )
    }
    const m = row.data
    const isSelf = m.auth_sub === user?.id
    const isThisOwner = m.role === "owner"
    return (
      <>
        {canManage && !isThisOwner && !isSelf && (
          <div className="flex items-center gap-1">
            {m.status === "suspended" ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  setReinstateTarget({
                    authSub: m.auth_sub,
                    name: memberName(m),
                  })
                }
              >
                {t("organization.members.reinstate")}
              </Button>
            ) : (
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  setSuspendTarget({ authSub: m.auth_sub, name: memberName(m) })
                }
              >
                {t("organization.members.suspend")}
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={() =>
                setRemoveTarget({ authSub: m.auth_sub, name: memberName(m) })
              }
            >
              {t("organization.members.remove")}
            </Button>
          </div>
        )}
        {isSelf && !isThisOwner && (
          <ConfirmationDialog
            render={<Button variant="ghost" size="sm" disabled={leaving} />}
            title={t("organization.members.leaveTitle")}
            description={t("organization.members.leaveDescription")}
            confirmLabel={t("organization.members.leave")}
            pending={leaving}
            onConfirm={() =>
              leaveOrganization(undefined, {
                onSuccess: () => void navigate({ to: "/organizations" }),
              })
            }
          >
            <IconDoorExit data-icon="inline-start" />
            {t("organization.members.leave")}
          </ConfirmationDialog>
        )}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-end gap-3">
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
          <SelectTrigger className="h-9 w-32 text-xs capitalize">
            <SelectValue />
          </SelectTrigger>
          <SelectContent className="capitalize">
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

      {isLoading || invitationsLoading ? (
        <div className="flex flex-col gap-2">
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-14 w-full rounded-xl" />
          ))}
        </div>
      ) : paged.length === 0 ? (
        <div className="rounded-xl border">
          <EmptyState
            icon={IconUsers}
            title={t("organization.members.noResults")}
          />
        </div>
      ) : (
        <>
          {/* Desktop: bordered table. */}
          <div className="hidden overflow-hidden rounded-xl border md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("organization.members.memberCol")}</TableHead>
                  <TableHead>{t("organization.members.roleCol")}</TableHead>
                  <TableHead>{t("organization.members.joinedCol")}</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {paged.map((row) => (
                  <TableRow key={row.key}>
                    <TableCell>
                      <div className="flex items-center gap-3">
                        {row.kind === "invitation" ? (
                          <>
                            <Avatar className="size-7 shrink-0 rounded-lg">
                              <AvatarFallback className="rounded-lg bg-muted text-muted-foreground">
                                <IconMail className="size-3.5" />
                              </AvatarFallback>
                            </Avatar>
                            <span className="flex items-center gap-1.5 text-sm font-medium text-muted-foreground">
                              {maskEmail(row.data.email)}
                              <Badge variant="outline" className="text-[10px]">
                                {t("organization.members.pendingBadge")}
                              </Badge>
                            </span>
                          </>
                        ) : (
                          <>
                            <Avatar className="size-7 shrink-0 rounded-lg">
                              <AvatarImage
                                src={row.data.avatar_url || undefined}
                                alt=""
                              />
                              <AvatarFallback className="rounded-lg text-xs">
                                {initials(
                                  row.data.full_name || row.data.email || "?"
                                )}
                              </AvatarFallback>
                            </Avatar>
                            <div className="flex flex-col">
                              <span className="flex items-center gap-1.5 text-sm font-medium">
                                {memberName(row.data)}
                                {row.data.auth_sub === user?.id && (
                                  <Badge
                                    variant="outline"
                                    className="text-[10px]"
                                  >
                                    {t("organization.members.you")}
                                  </Badge>
                                )}
                                {row.data.status === "suspended" && (
                                  <StatusBadge
                                    status="suspended"
                                    label={t(
                                      "organization.members.suspendedBadge"
                                    )}
                                  />
                                )}
                              </span>
                              <div className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                                {row.data.email && (
                                  <>
                                    <button
                                      type="button"
                                      onClick={() => {
                                        void navigator.clipboard.writeText(
                                          row.data.email!
                                        )
                                        toast.success(
                                          t("organization.members.emailCopied")
                                        )
                                      }}
                                      className="inline-flex items-center gap-1 rounded transition-colors hover:text-foreground"
                                    >
                                      <span>{maskEmail(row.data.email)}</span>
                                      <IconCopy className="size-3 shrink-0" />
                                    </button>
                                    <span>•</span>
                                  </>
                                )}
                                <button
                                  type="button"
                                  onClick={() => {
                                    void navigator.clipboard.writeText(
                                      row.data.auth_sub
                                    )
                                    toast.success(t("account.accountIdCopied"))
                                  }}
                                  className="inline-flex items-center gap-1 rounded font-mono transition-colors hover:text-foreground"
                                >
                                  {row.data.auth_sub.slice(0, 4)}...
                                  <IconCopy className="size-3 shrink-0" />
                                </button>
                              </div>
                            </div>
                          </>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <RoleControl row={row} />
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      <JoinedOrExpiry row={row} />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end">
                        <Actions row={row} />
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          {/* Mobile: compact contact cards — name/role up top, email as a
              subtitle, joined/actions on one muted line. Not the generic
              label/value stack — a member row reads fine without repeating
              "Email:"/"Role:"/"Joined:" for every entry. */}
          <div className="flex flex-col gap-2 md:hidden">
            {paged.map((row) => {
              const isInvitation = row.kind === "invitation"
              const name = isInvitation
                ? maskEmail(row.data.email)
                : memberName(row.data)
              const isSelf =
                row.kind === "member" && row.data.auth_sub === user?.id
              return (
                <div
                  key={row.key}
                  className="flex flex-col gap-2 rounded-xl border p-3"
                >
                  <div className="flex items-start gap-3">
                    {isInvitation ? (
                      <Avatar className="size-8 shrink-0 rounded-lg">
                        <AvatarFallback className="rounded-lg bg-muted text-muted-foreground">
                          <IconMail className="size-4" />
                        </AvatarFallback>
                      </Avatar>
                    ) : (
                      <Avatar className="size-8 shrink-0 rounded-lg">
                        <AvatarImage
                          src={row.data.avatar_url || undefined}
                          alt=""
                        />
                        <AvatarFallback className="rounded-lg text-xs">
                          {initials(name || "?")}
                        </AvatarFallback>
                      </Avatar>
                    )}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate text-sm font-medium">
                          {name}
                        </span>
                        {isSelf && (
                          <Badge
                            variant="outline"
                            className="shrink-0 text-[10px]"
                          >
                            {t("organization.members.you")}
                          </Badge>
                        )}
                        {isInvitation && (
                          <Badge
                            variant="outline"
                            className="shrink-0 text-[10px]"
                          >
                            {t("organization.members.pendingBadge")}
                          </Badge>
                        )}
                        {row.kind === "member" &&
                          row.data.status === "suspended" && (
                            <StatusBadge
                              status="suspended"
                              label={t("organization.members.suspendedBadge")}
                            />
                          )}
                      </div>
                      {row.kind === "member" && (
                        <div className="mt-0.5 flex flex-col items-start gap-0.5 text-xs text-muted-foreground">
                          {row.data.email && (
                            <button
                              type="button"
                              onClick={() => {
                                void navigator.clipboard.writeText(
                                  row.data.email!
                                )
                                toast.success(
                                  t("organization.members.emailCopied")
                                )
                              }}
                              className="inline-flex items-center gap-1 rounded transition-colors hover:text-foreground"
                            >
                              <span className="truncate">
                                {maskEmail(row.data.email)}
                              </span>
                              <IconCopy className="size-3 shrink-0" />
                            </button>
                          )}
                          <button
                            type="button"
                            onClick={() => {
                              void navigator.clipboard.writeText(
                                row.data.auth_sub
                              )
                              toast.success(t("account.accountIdCopied"))
                            }}
                            className="inline-flex items-center gap-1 rounded font-mono transition-colors hover:text-foreground"
                          >
                            {row.data.auth_sub.slice(0, 4)}...
                            <IconCopy className="size-3 shrink-0" />
                          </button>
                        </div>
                      )}
                    </div>
                    <RoleControl row={row} />
                  </div>
                  <div className="flex items-center justify-between gap-2 border-t pt-2">
                    <span className="text-xs text-muted-foreground">
                      <JoinedOrExpiry row={row} />
                    </span>
                    <Actions row={row} />
                  </div>
                </div>
              )
            })}
          </div>
        </>
      )}

      <DataTablePagination
        page={page}
        totalPages={totalPages}
        onPageChange={setPage}
      />

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

      {suspendTarget && (
        <ConfirmationDialog
          open
          onOpenChange={(open) => !open && setSuspendTarget(null)}
          render={<span className="hidden" />}
          nativeButton={false}
          title={t("organization.members.suspendTitle")}
          description={t("organization.members.suspendDescription", {
            name: suspendTarget.name,
          })}
          consequences={[t("organization.members.suspendConsequence")]}
          confirmLabel={t("organization.members.suspend")}
          pending={suspending}
          onConfirm={() =>
            suspendMember(suspendTarget.authSub, {
              onSuccess: () => setSuspendTarget(null),
            })
          }
        >
          {null}
        </ConfirmationDialog>
      )}

      {reinstateTarget && (
        <ConfirmationDialog
          open
          onOpenChange={(open) => !open && setReinstateTarget(null)}
          render={<span className="hidden" />}
          nativeButton={false}
          title={t("organization.members.reinstateTitle")}
          description={t("organization.members.reinstateDescription", {
            name: reinstateTarget.name,
          })}
          confirmLabel={t("organization.members.reinstate")}
          destructive={false}
          pending={reinstating}
          onConfirm={() =>
            reinstateMember(reinstateTarget.authSub, {
              onSuccess: () => setReinstateTarget(null),
            })
          }
        >
          {null}
        </ConfirmationDialog>
      )}

      {revokeTarget && (
        <ConfirmationDialog
          open
          onOpenChange={(open) => !open && setRevokeTarget(null)}
          render={<span className="hidden" />}
          nativeButton={false}
          title={t("organization.invitations.revokeTitle")}
          description={t("organization.invitations.revokeDescription", {
            email: revokeTarget.email,
          })}
          confirmLabel={t("organization.invitations.revoke")}
          pending={revoking}
          onConfirm={() =>
            revoke(revokeTarget.id, {
              onSuccess: () => {
                setRevokeTarget(null)
                toastWithUndo(t("organization.invitations.revoked"), () =>
                  reinvite({
                    email: revokeTarget.email,
                    role: revokeTarget.role,
                  })
                )
              },
            })
          }
        >
          {null}
        </ConfirmationDialog>
      )}

      {resendTarget && (
        <ConfirmationDialog
          open
          onOpenChange={(open) => !open && setResendTarget(null)}
          render={<span className="hidden" />}
          nativeButton={false}
          title={t("organization.invitations.resendTitle")}
          description={t("organization.invitations.resendDescription", {
            email: resendTarget.email,
          })}
          confirmLabel={t("organization.invitations.resend")}
          pending={resending}
          onConfirm={() =>
            resend(
              { email: resendTarget.email, role: resendTarget.role },
              { onSuccess: () => setResendTarget(null) }
            )
          }
        >
          {null}
        </ConfirmationDialog>
      )}
    </div>
  )
}
