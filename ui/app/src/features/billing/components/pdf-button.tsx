import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2, IconFileDownload } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { API } from "@/lib/api/path"
import { downloadFile } from "@/lib/api/download"

interface Props {
  invoiceId: string
  organizationId: string
}

export function PdfButton({ invoiceId, organizationId }: Props) {
  const { t, i18n } = useTranslation()
  const { mutate, isPending } = useMutation({
    mutationFn: () =>
      downloadFile(
        `${API.billing(organizationId, "invoices", invoiceId, "pdf")}?lang=${i18n.language}`,
        `invoice-${invoiceId.slice(0, 8)}.pdf`,
        "arraybuffer"
      ),
    onError: () => toast.error(t("billing.invoices.pdfFailed")),
  })

  return (
    <Button
      size="sm"
      variant="ghost"
      disabled={isPending}
      onClick={() => mutate()}
      title={t("billing.invoices.downloadPdf")}
    >
      {isPending ? (
        <IconLoader2 className="size-4 animate-spin" />
      ) : (
        <IconFileDownload className="size-4" />
      )}
    </Button>
  )
}
