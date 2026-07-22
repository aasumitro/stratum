import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconTrash, IconPlayerPause, IconPlayerPlay } from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"
import {
  useDeleteOrganization,
  useTransferOwnership,
  useSuspendOrganization,
  useUnsuspendOrganization,
  useOrganizationMembers,
  useWebhooks,
} from "@/features/organization/hooks"
import { useUsage } from "@/features/billing/hooks"
import { formatBytes } from "@/lib/format"

interface Props {
  organizationId: string
  slug: string
  status: string
}

// Danger zone card: transfer ownership (atomic, checkbox-gated),
// suspend/unsuspend (self-service, reversible), delete (30-day soft
// delete, type-slug-to-confirm, consequences enumerated) — was previously
// just a delete-only card; transfer lived only in the members table, and
// suspend had no owner-facing UI at all.
export function OrganizationDangerZone({
  organizationId,
  slug,
  status,
}: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const { data: membersData } = useOrganizationMembers(organizationId)
  const { data: webhooksData } = useWebhooks(organizationId)
  const { data: usageData } = useUsage(organizationId)
  const members = membersData?.data ?? []
  const webhooks = webhooksData?.data ?? []
  const storageBytes =
    usageData?.data?.find((u) => u.metric === "storage_bytes")?.value ?? 0

  const [transferOpen, setTransferOpen] = useState(false)
  const [transferTarget, setTransferTarget] = useState("")
  const [transferAck, setTransferAck] = useState(false)
  const { mutate: transferOwnership, isPending: transferring } =
    useTransferOwnership(organizationId)

  const { mutate: suspend, isPending: suspending } =
    useSuspendOrganization(organizationId)
  const { mutate: unsuspend, isPending: unsuspending } =
    useUnsuspendOrganization(organizationId)

  const { mutate: deleteOrganization, isPending: deleting } =
    useDeleteOrganization(organizationId)

  const admins = members.filter((m) => m.role === "admin")
  const isSuspended = status === "suspended"

  return (
    <Card className="border-destructive/30">
      <CardHeader>
        <CardTitle className="text-destructive">
          {t("organization.danger.title")}
        </CardTitle>
        <CardDescription>
          {t("organization.danger.description")}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col divide-y">
        {/* Transfer ownership — atomic, no acceptance step */}
        <div className="flex flex-col gap-2 py-4 first:pt-0 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <div>
            <p className="text-sm font-medium">
              {t("organization.danger.transferOwnership")}
            </p>
            <p className="text-sm text-muted-foreground">
              {t("organization.danger.transferDescription")}
            </p>
          </div>
          <Dialog open={transferOpen} onOpenChange={setTransferOpen}>
            <DialogTrigger
              render={
                <Button variant="outline" size="sm" disabled={!admins.length} />
              }
            >
              {t("organization.danger.transferAction")}
            </DialogTrigger>
            <DialogContent className="sm:max-w-sm">
              <DialogHeader>
                <DialogTitle>
                  {t("organization.danger.transferAction")}
                </DialogTitle>
                <DialogDescription>
                  {t("organization.danger.transferModalDescription")}
                </DialogDescription>
              </DialogHeader>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="transfer-target">
                  {t("organization.danger.transferNewOwnerLabel")}
                </Label>
                <Select
                  value={transferTarget}
                  onValueChange={(v) => setTransferTarget(v ?? "")}
                >
                  <SelectTrigger id="transfer-target">
                    <SelectValue
                      placeholder={t(
                        "organization.danger.transferSelectPlaceholder"
                      )}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {admins.map((m) => (
                      <SelectItem key={m.auth_sub} value={m.auth_sub}>
                        {m.full_name || m.email} —{" "}
                        {t("organization.roles.admin")}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex items-start gap-2 pt-2">
                <Checkbox
                  id="transfer-ack"
                  checked={transferAck}
                  onCheckedChange={(v) => setTransferAck(v === true)}
                />
                <Label htmlFor="transfer-ack" className="text-sm font-normal">
                  {t("organization.danger.transferAck")}
                </Label>
              </div>
              <DialogFooter>
                <Button
                  variant="outline"
                  onClick={() => setTransferOpen(false)}
                >
                  {t("common.cancel")}
                </Button>
                <Button
                  disabled={!transferTarget || !transferAck || transferring}
                  onClick={() =>
                    transferOwnership(
                      { auth_sub: transferTarget },
                      {
                        onSuccess: () => {
                          setTransferOpen(false)
                          setTransferTarget("")
                          setTransferAck(false)
                        },
                      }
                    )
                  }
                >
                  {t("organization.danger.transferConfirm")}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>

        {/* Suspend / unsuspend — reversible, read-only for all while active */}
        <div className="flex flex-col gap-2 py-4 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <div>
            <p className="text-sm font-medium">
              {isSuspended
                ? t("organization.danger.unsuspendOrganization")
                : t("organization.danger.suspendOrganization")}
            </p>
            <p className="text-sm text-muted-foreground">
              {t("organization.danger.suspendDescription")}
            </p>
          </div>
          {isSuspended ? (
            <Button
              variant="outline"
              size="sm"
              disabled={unsuspending}
              onClick={() => unsuspend()}
            >
              <IconPlayerPlay data-icon="inline-start" />
              {t("organization.danger.unsuspendAction")}
            </Button>
          ) : (
            <ConfirmationDialog
              render={<Button variant="outline" size="sm" />}
              title={t("organization.danger.suspendConfirmTitle")}
              description={t("organization.danger.suspendConfirmDescription")}
              confirmLabel={t("organization.danger.suspendAction")}
              destructive={false}
              pending={suspending}
              onConfirm={() => suspend({})}
            >
              <IconPlayerPause data-icon="inline-start" />
              {t("organization.danger.suspendAction")}
            </ConfirmationDialog>
          )}
        </div>

        {/* Delete — 30-day soft delete, consequences enumerated, type-to-confirm */}
        <div className="flex flex-col gap-2 py-4 last:pb-0 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <div>
            <p className="text-sm font-medium text-destructive">
              {t("organization.danger.deleteOrganization")}
            </p>
            <p className="text-sm text-muted-foreground">
              {t("organization.danger.deleteIrreversible")}
            </p>
          </div>
          <ConfirmationDialog
            render={
              <Button variant="destructive" size="sm" disabled={deleting} />
            }
            title={t("organization.danger.deleteTitle")}
            description={t("organization.danger.deleteConfirmDescription")}
            consequences={[
              t("organization.danger.deleteConsequenceMembers", {
                count: members.length,
              }),
              t("organization.danger.deleteConsequenceStorage", {
                size: formatBytes(storageBytes),
              }),
              t("organization.danger.deleteConsequenceWebhooks", {
                count: webhooks.length,
              }),
            ]}
            confirmPhrase={slug}
            confirmPhraseLabel={t("organization.danger.deleteTypeToConfirm", {
              slug,
            })}
            confirmLabel={t("organization.danger.deleteConfirm")}
            pending={deleting}
            onConfirm={() =>
              deleteOrganization(undefined, {
                onSuccess: () => void navigate({ to: "/organizations" }),
              })
            }
          >
            <IconTrash data-icon="inline-start" />
            {t("organization.danger.deleteOrganization")}
          </ConfirmationDialog>
        </div>
      </CardContent>
    </Card>
  )
}
