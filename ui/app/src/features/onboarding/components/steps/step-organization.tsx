import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconArrowRight } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { CreateOrganizationForm } from "@/features/organization/components/create-organization-form"
import { PendingInvitationCard } from "@/features/organization/components/pending-invitation-card"
import { JoinOrganizationForm } from "./join-organization-form"
import { useMyInvitations } from "@/features/organization/hooks"
import { useCompleteOnboarding } from "@/features/onboarding/hooks/use-complete-onboarding"

type View = "list" | "create"

export function StepOrganization() {
  const { t } = useTranslation()
  const [view, setView] = useState<View>("list")
  const [declinedIds, setDeclinedIds] = useState<Set<string>>(new Set())
  const { data, isLoading } = useMyInvitations()
  const invitations = (data?.data ?? []).filter(
    (inv) => !declinedIds.has(inv.id)
  )
  const { complete, isPending: completing } = useCompleteOnboarding()

  if (view === "create") {
    return (
      <CreateOrganizationForm
        onBack={() => setView("list")}
        onCreated={(org) =>
          complete(
            org.id,
            t("onboarding.organization.createdToast", { name: org.name })
          )
        }
      />
    )
  }

  return (
    <div className="mx-auto flex w-full max-w-lg flex-col gap-6">
      <div>
        <h2 className="text-xl font-extrabold">
          {t("onboarding.organization.title")}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("onboarding.organization.subtitle")}
        </p>
      </div>

      {!isLoading && invitations.length > 0 && (
        <div className="flex flex-col gap-2">
          {invitations.map((inv) => (
            <PendingInvitationCard
              key={inv.id}
              invitation={inv}
              disabled={completing}
              onAccepted={() =>
                complete(
                  inv.organization_id,
                  t("onboarding.organization.joinedToast", {
                    name: inv.organization_name,
                  })
                )
              }
              onDeclined={() =>
                setDeclinedIds((prev) => new Set(prev).add(inv.id))
              }
            />
          ))}
        </div>
      )}

      <div className="flex items-center justify-between rounded-xl border p-3">
        <div>
          <p className="text-sm font-semibold">
            {t("onboarding.organization.createCardTitle")}
          </p>
          <p className="text-xs text-muted-foreground">
            {t("onboarding.organization.createCardSubtitle")}
          </p>
        </div>
        <Button type="button" onClick={() => setView("create")}>
          {t("onboarding.organization.createCardAction")}
          <IconArrowRight className="ml-1.5 size-4" />
        </Button>
      </div>

      <JoinOrganizationForm
        onJoined={(org) =>
          complete(
            org.id,
            t("onboarding.organization.joinedToast", { name: org.name })
          )
        }
      />
    </div>
  )
}
