import { useMemo, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconPlus, IconDoorEnter, IconBuilding } from "@tabler/icons-react"
import type { OrganizationView } from "@/types/organization"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusBadge } from "@/components/shared/status-badge"
import { DataTable, type DataTableColumn } from "@/components/shared/data-table"
import { CreateOrganizationDialog } from "@/features/organization/components/create-organization-dialog"
import { JoinOrganizationDialog } from "@/features/organization/components/join-organization-dialog"
import { PendingInvitationCard } from "@/features/organization/components/pending-invitation-card"
import { useOrganizations } from "@/features/organization/hooks/use-organization"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { useMyInvitations } from "@/features/organization/hooks/use-invitations"
import { useBillingSubscription, usePlans } from "@/features/billing/hooks"
import { useUpdatePreferences } from "@/features/account/hooks"
import { queryKeys } from "@/lib/api/keys"

function daysLeft(iso?: string): number {
  if (!iso) return 0
  const ms = new Date(iso).getTime() - Date.now()
  return ms > 0 ? Math.ceil(ms / (1000 * 60 * 60 * 24)) : 0
}

// One members + one subscription request per row (N+1) — GET
// /organizations has no bulk members/plan projection. Fine at "orgs one
// user belongs to" scale (a handful); revisit with a batch endpoint if this
// list ever needs to show hundreds of rows.
function MembersCell({ organizationId }: { organizationId: string }) {
  const { data, isLoading } = useOrganizationMembers(organizationId)
  if (isLoading) return <Skeleton className="h-4 w-6" />
  return <span>{data?.data?.length ?? "—"}</span>
}

function PlanCell({
  organizationId,
  planNames,
}: {
  organizationId: string
  planNames: Record<string, string>
}) {
  const { t } = useTranslation()
  const { data, isLoading } = useBillingSubscription(organizationId)
  const sub = data?.data
  if (isLoading) return <Skeleton className="h-5 w-16 rounded-full" />
  if (!sub) return <span className="text-muted-foreground">—</span>

  const label = planNames[sub.plan] ?? sub.plan
  const suffix =
    sub.status === "trialing" && sub.trial_end
      ? ` · ${t("organization.picker.trialDaysLeft", { days: daysLeft(sub.trial_end) })}`
      : ""

  return <StatusBadge status={sub.status} label={`${label}${suffix}`} />
}

export function OrganizationPickerPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [joinOpen, setJoinOpen] = useState(false)
  const [search, setSearch] = useState("")
  const [declinedInvitationIds, setDeclinedInvitationIds] = useState<
    Set<string>
  >(new Set())

  const { data, isLoading } = useOrganizations()
  const { data: plansData } = usePlans()
  const { data: myInvitationsData } = useMyInvitations()
  const { mutate: updatePreferences } = useUpdatePreferences()

  const pendingInvitations = (myInvitationsData?.data ?? []).filter(
    (inv) => !declinedInvitationIds.has(inv.id)
  )

  function handleInvitationAccepted(organizationName: string) {
    toast.success(
      t("onboarding.organization.joinedToast", { name: organizationName })
    )
    void queryClient.invalidateQueries({
      queryKey: queryKeys.organizations.list(),
    })
    void queryClient.invalidateQueries({
      queryKey: queryKeys.account.myInvitations(),
    })
  }

  const planNames = useMemo(
    () =>
      Object.fromEntries((plansData?.data ?? []).map((p) => [p.id, p.name])),
    [plansData]
  )

  const allOrganizations = data?.data ?? []
  const organizations = search
    ? allOrganizations.filter(
        (o) =>
          o.name.toLowerCase().includes(search.toLowerCase()) ||
          o.slug.includes(search.toLowerCase())
      )
    : allOrganizations

  async function openOrganization(org: OrganizationView) {
    localStorage.setItem("active_organization_id", org.id)
    updatePreferences({ default_organization_id: org.id })
    await navigate({
      to: "/organization/$organizationId",
      params: { organizationId: org.id },
    })
  }

  const columns: DataTableColumn<OrganizationView>[] = [
    {
      key: "name",
      header: t("organization.picker.orgCol"),
      cell: (o) => (
        <div className="flex items-center gap-2">
          <span className="font-medium">{o.name}</span>
          {o.status === "suspended" && <StatusBadge status="suspended" />}
        </div>
      ),
    },
    {
      key: "role",
      header: t("organization.picker.roleCol"),
      cell: (o) => (
        <span className="rounded-full border px-2.5 py-0.5 text-xs font-medium capitalize">
          {o.role}
        </span>
      ),
    },
    {
      key: "members",
      header: t("organization.picker.membersCol"),
      cell: (o) => <MembersCell organizationId={o.id} />,
    },
    {
      key: "plan",
      header: t("organization.picker.planCol"),
      cell: (o) => <PlanCell organizationId={o.id} planNames={planNames} />,
    },
    {
      key: "action",
      header: "",
      headerClassName: "text-right",
      className: "text-right",
      cell: (o) => (
        <Button
          size="sm"
          variant="outline"
          className={
            o.status === "suspended"
              ? "text-destructive hover:text-destructive"
              : undefined
          }
          onClick={() => void openOrganization(o)}
        >
          {o.status === "suspended"
            ? t("organization.picker.resolve")
            : t("organization.picker.open")}
        </Button>
      ),
    },
  ]

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-3">
        <div>
          <h1 className="text-2xl font-bold">
            {t("organization.picker.title")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("organization.picker.subtitle")}
          </p>
        </div>
        <div className="flex flex-1 flex-wrap items-center justify-end gap-2">
          {allOrganizations.length > 3 && (
            <Input
              placeholder={t("organization.picker.search")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="max-w-56"
            />
          )}
          <Button variant="outline" onClick={() => setJoinOpen(true)}>
            <IconDoorEnter data-icon="inline-start" />
            {t("organization.inviteCode.joinBtn")}
          </Button>
          <Button onClick={() => setCreateOpen(true)}>
            <IconPlus data-icon="inline-start" />
            {t("organization.picker.create")}
          </Button>
        </div>
      </div>

      {pendingInvitations.length > 0 && (
        <div className="flex flex-col gap-2">
          {pendingInvitations.map((inv) => (
            <PendingInvitationCard
              key={inv.id}
              invitation={inv}
              disabled={false}
              onAccepted={() => handleInvitationAccepted(inv.organization_name)}
              onDeclined={() =>
                setDeclinedInvitationIds((prev) => new Set(prev).add(inv.id))
              }
            />
          ))}
        </div>
      )}

      <DataTable
        columns={columns}
        rows={organizations}
        rowKey={(o) => o.id}
        isLoading={isLoading}
        empty={{
          icon: IconBuilding,
          title: t("organization.picker.empty"),
          description: t("organization.picker.emptySubtitle"),
        }}
      />

      <p className="text-xs text-muted-foreground">
        {t("organization.picker.lastOpenedNote")}
      </p>

      <CreateOrganizationDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
      />
      <JoinOrganizationDialog open={joinOpen} onOpenChange={setJoinOpen} />
    </div>
  )
}
