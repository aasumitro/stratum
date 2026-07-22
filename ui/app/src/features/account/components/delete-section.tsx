import { useState, useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { IconLoader2, IconTrash } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { useAuth } from "@/components/auth-provider"
import { usePollTask, useDeleteAccount } from "@/features/account/hooks"
import { useOrganizations } from "@/features/organization/hooks"

export function DeleteSection() {
  const { t } = useTranslation()
  const { signOut } = useAuth()
  const navigate = useNavigate()
  const [deleteTaskId, setDeleteTaskId] = useState<string | null>(null)

  const { data: deleteTask } = usePollTask(deleteTaskId)
  const { mutate: deleteAccount, isPending } = useDeleteAccount()
  const { data: orgsData } = useOrganizations()

  // Delete pre-check runs BEFORE the confirm dialog even opens (2f) —
  // owning any organization blocks deletion outright, with a deep link to
  // transfer or delete it first, instead of failing after the user commits.
  const ownedOrgs = (orgsData?.data ?? []).filter((o) => o.role === "owner")

  const taskStatus = deleteTask?.data?.status
  const isPolling =
    !!deleteTaskId && taskStatus !== "completed" && taskStatus !== "failed"

  useEffect(() => {
    if (!taskStatus) return
    if (taskStatus === "completed") {
      toast.success(t("account.dangerZone.deleted"))
      void signOut().then(() => navigate({ to: "/login" }))
    } else if (taskStatus === "failed") {
      toast.error(t("account.dangerZone.deletionFailed"))
    }
  }, [taskStatus, navigate, signOut, t])

  if (ownedOrgs.length > 0) {
    return (
      <div className="flex flex-col gap-2">
        <p className="text-sm font-medium text-destructive">
          {t("account.dangerZone.deleteTitle")}
        </p>
        <p className="text-sm text-muted-foreground">
          {t("account.dangerZone.blockedByOwnership")}
        </p>
        <ul className="flex flex-col gap-1">
          {ownedOrgs.map((org) => (
            <li key={org.id}>
              <a
                href={`/organization/${org.id}/settings`}
                className="text-sm font-medium text-primary underline underline-offset-4"
              >
                {t("account.dangerZone.transferOrDelete", { name: org.name })}
              </a>
            </li>
          ))}
        </ul>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
      <div>
        <p className="text-sm font-medium text-destructive">
          {t("account.dangerZone.deleteTitle")}
        </p>
        <p className="text-sm text-muted-foreground">
          {t("account.dangerZone.deleteDescription")}
        </p>
      </div>
      {isPending || isPolling ? (
        <Button variant="destructive" size="sm" disabled className="shrink-0">
          <IconLoader2 data-icon="inline-start" className="animate-spin" />
          {t("account.dangerZone.deleteAccount")}
        </Button>
      ) : (
        <AlertDialog>
          <AlertDialogTrigger
            className="shrink-0"
            render={<Button variant="destructive" size="sm" />}
          >
            <IconTrash data-icon="inline-start" />
            {t("account.dangerZone.deleteAccount")}
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t("account.dangerZone.deleteConfirmTitle")}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t("account.dangerZone.deleteConfirmDescription")}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                onClick={() =>
                  deleteAccount(undefined, {
                    onSuccess: (res) => {
                      if (res.data?.id) {
                        setDeleteTaskId(res.data.id)
                        toast.info(t("account.dangerZone.deleting"))
                      } else {
                        void signOut().then(() => navigate({ to: "/login" }))
                      }
                    },
                    onError: () =>
                      toast.error(
                        t("account.dangerZone.accountDeletionFailed")
                      ),
                  })
                }
              >
                {t("account.dangerZone.deleteConfirm")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </div>
  )
}
