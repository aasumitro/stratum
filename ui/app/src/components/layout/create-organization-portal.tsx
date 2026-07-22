import { useSyncExternalStore } from "react"
import { CreateOrganizationDialog } from "@/features/organization/components/create-organization-dialog"
import { useTransientStore } from "@/hooks/use-transient"

export function CreateOrganizationPortal() {
  const { values } = useSyncExternalStore(
    useTransientStore.subscribe.bind(useTransientStore),
    useTransientStore.getState.bind(useTransientStore)
  )
  const open = values["create-organization"] === "open"
  return (
    <CreateOrganizationDialog
      open={open}
      onOpenChange={(v) =>
        useTransientStore.setValue(
          "create-organization",
          v ? "open" : undefined
        )
      }
    />
  )
}
