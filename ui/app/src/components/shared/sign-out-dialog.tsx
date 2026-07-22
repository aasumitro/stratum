import { useSyncExternalStore, useState } from "react"
import { useRouter } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2 } from "@tabler/icons-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { useAuth } from "@/components/auth-provider"
import { supabase } from "@/lib/auth/supabase"
import { useTransientStore } from "@/hooks/use-transient"

type Scope = "local" | "global"

function useTransient() {
  return useSyncExternalStore(
    useTransientStore.subscribe.bind(useTransientStore),
    useTransientStore.getState.bind(useTransientStore)
  )
}

export function SignOutDialog() {
  const { t } = useTranslation()
  const { signOut } = useAuth()
  const router = useRouter()
  const { values } = useTransient()
  const [pending, setPending] = useState(false)

  const scope = values["signout-scope"] as Scope | undefined
  const open = !!scope

  function handleClose() {
    useTransientStore.setValue("signout-scope", undefined)
  }

  async function handleConfirm() {
    setPending(true)
    try {
      if (scope === "global") {
        await supabase.auth.signOut({ scope: "global" })
      }
      await signOut()
      toast.success(
        scope === "global" ? t("auth.signOut.allDone") : t("auth.signOut.done")
      )
      handleClose()
      void router.navigate({ to: "/login" })
    } catch {
      toast.error(t("auth.signOut.failed"))
      setPending(false)
    }
  }

  const isGlobal = scope === "global"

  return (
    <AlertDialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {isGlobal ? t("auth.signOut.globalTitle") : t("auth.signOut.title")}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {isGlobal
              ? t("auth.signOut.globalDescription")
              : t("auth.signOut.description")}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>
            {t("common.cancel")}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending}
            onClick={() => void handleConfirm()}
          >
            {pending && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {isGlobal
              ? t("auth.signOut.confirmAll")
              : t("auth.signOut.confirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
