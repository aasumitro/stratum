import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2 } from "@tabler/icons-react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { parseApiError } from "@/lib/api/error"
import {
  useInviteCodePreview,
  useJoinOrganization,
} from "@/features/organization/hooks"

interface Props {
  open: boolean
  onOpenChange: (v: boolean) => void
}

export function JoinOrganizationDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [code, setCode] = useState("")
  const [checkedCode, setCheckedCode] = useState<string | null>(null)
  const [serverError, setServerError] = useState<string | null>(null)

  const {
    data: preview,
    isLoading: checking,
    error: previewError,
  } = useInviteCodePreview(checkedCode ?? "")
  const { mutate: join, isPending: joining } = useJoinOrganization()

  function handleOpenChange(v: boolean) {
    if (!v) {
      setCode("")
      setCheckedCode(null)
      setServerError(null)
    }
    onOpenChange(v)
  }

  function goToExistingOrganization(organizationId: string) {
    handleOpenChange(false)
    void navigate({
      to: "/organization/$organizationId",
      params: { organizationId },
    })
  }

  function confirmJoin() {
    if (!checkedCode) return
    setServerError(null)
    join(
      { code: checkedCode },
      {
        onSuccess: (res) => {
          toast.success(t("organization.inviteCode.joinedTitle"))
          handleOpenChange(false)
          const orgId = res.data?.id
          if (orgId)
            void navigate({
              to: "/organization/$organizationId",
              params: { organizationId: orgId },
            })
        },
        onError: (err) =>
          setServerError(
            parseApiError(err, t("organization.inviteCode.joinFailed"))
          ),
      }
    )
  }

  const org = checkedCode && preview?.data ? preview.data : null
  // Preview now rejects a code that resolves to an organization the caller
  // already belongs to (JOIN_ALREADY_MEMBER) instead of letting the check
  // step succeed and only failing on confirm — surface that state here
  // instead of the generic error text so the dialog can offer "go to
  // organization" instead of a dead-end confirm button.
  const alreadyMemberOrgId =
    checkedCode && previewError?.status?.code === "JOIN_ALREADY_MEMBER"
      ? (
          previewError.status.details as
            | { organization_id?: string }
            | undefined
        )?.organization_id
      : undefined

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t("onboarding.organization.joinCardTitle")}
          </DialogTitle>
          <DialogDescription>
            {t("onboarding.organization.joinCardSubtitle")}
          </DialogDescription>
        </DialogHeader>

        {org ? (
          <>
            <div className="flex items-center gap-3 rounded-xl border p-3">
              <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-sm font-semibold text-primary">
                {org.organization_name.charAt(0).toUpperCase()}
              </div>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold">
                  {org.organization_name}
                </p>
                {(org.owner_name || org.owner_email) && (
                  <p className="truncate text-xs text-muted-foreground">
                    {t("onboarding.organization.ownedBy", {
                      owner: org.owner_name || org.owner_email,
                    })}
                  </p>
                )}
              </div>
            </div>
            {serverError && (
              <p role="alert" className="text-xs text-destructive">
                {serverError}
              </p>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={joining}
                onClick={() => setCheckedCode(null)}
              >
                {t("onboarding.organization.changeCode")}
              </Button>
              <Button type="button" disabled={joining} onClick={confirmJoin}>
                {joining && (
                  <IconLoader2
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                )}
                {t("onboarding.organization.confirmJoin")}
              </Button>
            </DialogFooter>
          </>
        ) : alreadyMemberOrgId ? (
          <>
            <p className="text-sm text-muted-foreground">
              {t("organization.inviteCode.alreadyMember")}
            </p>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setCheckedCode(null)}
              >
                {t("onboarding.organization.changeCode")}
              </Button>
              <Button
                type="button"
                onClick={() => goToExistingOrganization(alreadyMemberOrgId)}
              >
                {t("organization.invitations.goToOrganization")}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="join-invite-code">
                {t("onboarding.organization.inviteCode")}
              </Label>
              <Input
                id="join-invite-code"
                placeholder={t("onboarding.organization.inviteCodePlaceholder")}
                value={code}
                onChange={(e) => {
                  setServerError(null)
                  setCode(e.target.value)
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && code.trim().length === 8)
                    setCheckedCode(code.trim())
                }}
              />
              {(serverError || previewError) && (
                <p role="alert" className="text-xs text-destructive">
                  {serverError ??
                    parseApiError(
                      previewError,
                      t("onboarding.organization.joinFailed")
                    )}
                </p>
              )}
            </div>
            <DialogFooter>
              <Button
                type="button"
                disabled={code.trim().length !== 8 || checking}
                onClick={() => setCheckedCode(code.trim())}
              >
                {checking && (
                  <IconLoader2
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                )}
                {t("onboarding.organization.checkCode")}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
