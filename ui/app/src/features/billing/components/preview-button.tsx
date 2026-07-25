import { useRef } from "react"
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2, IconEye } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { API } from "@/lib/api/path"
import { fetchBlobUrl } from "@/lib/api/download"

interface Props {
  invoiceId: string
  organizationId: string
}

export function PreviewButton({ invoiceId, organizationId }: Props) {
  const { t, i18n } = useTranslation()
  const winRef = useRef<Window | null>(null)

  const { mutate, isPending } = useMutation({
    mutationFn: () =>
      fetchBlobUrl(
        `${API.billing(organizationId, "invoices", invoiceId, "pdf")}?lang=${i18n.language}`,
        "application/pdf"
      ),
    onMutate: () => {
      // Open synchronously in the click handler to bypass popup blockers —
      // same pattern as pay-button/regenerate-button.
      winRef.current = window.open("", "_blank")
    },
    onSuccess: (blobUrl) => {
      if (winRef.current && !winRef.current.closed) {
        winRef.current.location.href = blobUrl
      } else {
        winRef.current?.close()
        window.open(blobUrl, "_blank")
      }
      winRef.current = null
      setTimeout(() => URL.revokeObjectURL(blobUrl), 60_000)
    },
    onError: () => {
      winRef.current?.close()
      winRef.current = null
      toast.error(t("billing.invoices.previewFailed"))
    },
  })

  return (
    <Button
      size="sm"
      variant="outline"
      disabled={isPending}
      onClick={() => mutate()}
    >
      {isPending ? (
        <IconLoader2 data-icon="inline-start" className="animate-spin" />
      ) : (
        <IconEye data-icon="inline-start" />
      )}
      {t("billing.invoices.previewPdf")}
    </Button>
  )
}
