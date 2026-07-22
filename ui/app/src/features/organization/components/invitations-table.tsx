import { useTranslation } from "react-i18next"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusBadge } from "@/components/shared/status-badge"
import { toastWithUndo } from "@/components/shared/toast-with-undo"
import {
  useOrganizationInvitations,
  useRevokeInvitation,
  useResendInvitation,
  useInvite,
} from "@/features/organization/hooks"

interface Props {
  organizationId: string
  canManage: boolean
}

function isExpired(iso: string): boolean {
  return new Date(iso).getTime() < Date.now()
}

export function InvitationsTable({ organizationId, canManage }: Props) {
  const { t } = useTranslation()

  const { data, isLoading } = useOrganizationInvitations(
    organizationId,
    canManage
  )
  const { mutate: revoke } = useRevokeInvitation(organizationId)
  const { mutate: resend, isPending: resending } =
    useResendInvitation(organizationId)
  const { mutate: reinvite } = useInvite(organizationId)

  const invitations = data?.data ?? []

  if (!canManage) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("organization.invitations.adminsOnly")}
      </p>
    )
  }

  function handleRevoke(id: string, email: string, role: string) {
    revoke(id, {
      onSuccess: () =>
        // Revoke is instant/confirm-less — recoverable by re-inviting (1k).
        toastWithUndo(t("organization.invitations.revoked"), () =>
          reinvite({ email, role: role as never })
        ),
    })
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        {invitations.length === 1
          ? t("organization.invitations.count", { count: invitations.length })
          : t("organization.invitations.countPlural", {
              count: invitations.length,
            })}
      </p>

      <div className="rounded-xl border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("organization.invitations.emailCol")}</TableHead>
              <TableHead>{t("organization.invitations.roleCol")}</TableHead>
              <TableHead>{t("organization.invitations.expiresCol")}</TableHead>
              <TableHead className="hidden w-40 md:table-cell" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              Array.from({ length: 2 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell>
                    <Skeleton className="h-4 w-40" />
                  </TableCell>
                  <TableCell>
                    <Skeleton className="h-4 w-16" />
                  </TableCell>
                  <TableCell>
                    <Skeleton className="h-4 w-24" />
                  </TableCell>
                  <TableCell />
                </TableRow>
              ))
            ) : invitations.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={4}
                  className="py-8 text-center text-sm text-muted-foreground"
                >
                  {t("organization.invitations.noInvitations")}
                </TableCell>
              </TableRow>
            ) : (
              invitations.map((inv) => {
                const expired = isExpired(inv.expires_at)
                return (
                  <TableRow key={inv.id}>
                    <TableCell className="text-sm font-medium">
                      {inv.email}
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className="capitalize">
                        {inv.role}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {expired ? (
                        <StatusBadge
                          status="expired"
                          label={t("organization.invitations.expired")}
                        />
                      ) : (
                        new Date(inv.expires_at).toLocaleDateString()
                      )}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={resending}
                          onClick={() =>
                            resend({ email: inv.email, role: inv.role })
                          }
                        >
                          {t("organization.invitations.resend")}
                        </Button>
                        {/* Instant, confirm-less — recoverable via the Undo
                            toast (1k), not a confirmation dialog. */}
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive hover:text-destructive"
                          onClick={() =>
                            handleRevoke(inv.id, inv.email, inv.role)
                          }
                        >
                          {t("organization.invitations.revoke")}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
