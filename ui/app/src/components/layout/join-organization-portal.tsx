import { useSyncExternalStore } from "react"
import { JoinOrganizationDialog } from "@/features/organization/components/join-organization-dialog"
import { useTransientStore } from "@/hooks/use-transient"

export function JoinOrganizationPortal() {
  const { values } = useSyncExternalStore(
    useTransientStore.subscribe.bind(useTransientStore),
    useTransientStore.getState.bind(useTransientStore)
  )
  const open = values["join-organization"] === "open"
  return (
    <JoinOrganizationDialog
      open={open}
      onOpenChange={(v) =>
        useTransientStore.setValue("join-organization", v ? "open" : undefined)
      }
    />
  )
}
