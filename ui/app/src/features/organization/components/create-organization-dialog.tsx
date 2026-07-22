import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"
import { queryKeys } from "@/lib/api/keys"
import { capture } from "@/lib/analytics"
import { cn } from "@/lib/ui"
import {
  CreateOrganizationForm,
  type InnerStep,
} from "@/features/organization/components/create-organization-form"

interface Props {
  open: boolean
  onOpenChange: (v: boolean) => void
}

export function CreateOrganizationDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // CreateOrganizationForm owns its form state internally, so a plain
  // open/close toggle can't reset it — remounting via a changing key is
  // what forces a fresh form the next time the dialog opens.
  const [instanceKey, setInstanceKey] = useState(0)
  // Only Review needs the wide two-column layout — Details/Plan stay
  // narrow so the dialog doesn't sit half-empty on those steps.
  const [step, setStep] = useState<InnerStep>("details")

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          setInstanceKey((k) => k + 1)
          setStep("details")
        }
        onOpenChange(v)
      }}
    >
      <DialogContent
        className={cn(
          "max-h-[90vh] overflow-y-auto",
          step === "review" ? "sm:max-w-4xl" : "sm:max-w-lg"
        )}
      >
        {/* Visually hidden: CreateOrganizationForm renders its own
            per-step heading ("Organization details" / "Choose your plan" /
            "Review & confirm"), which is the only title sighted users need.
            This one stays for the dialog's accessible name/description. */}
        <DialogHeader className="sr-only">
          <DialogTitle>{t("dashboard.newOrganization")}</DialogTitle>
          <DialogDescription>
            {t("organization.create.description")}
          </DialogDescription>
        </DialogHeader>

        <CreateOrganizationForm
          key={instanceKey}
          hideStepIndicator
          onStepChange={setStep}
          onBack={() => onOpenChange(false)}
          onCreated={() => {
            toast.success(t("organization.create.created"))
            capture("organization_created")
            onOpenChange(false)
            void queryClient.invalidateQueries({
              queryKey: queryKeys.organizations.list(),
            })
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
