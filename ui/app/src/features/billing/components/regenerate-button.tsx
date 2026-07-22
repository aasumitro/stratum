import { useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2, IconRefresh } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { useHTTPActionPost } from "@/lib/api/action"
import { API } from "@/lib/api/path"
import { queryKeys } from "@/lib/api/keys"
import type { PaymentLink } from "@/types/billing"

interface Props {
  invoiceId: string
  organizationId: string
}

export function RegenerateButton({ invoiceId, organizationId }: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [opening, setOpening] = useState(false)
  const winRef = useRef<Window | null>(null)

  const { mutate } = useHTTPActionPost<PaymentLink>({
    url: API.billing(
      organizationId,
      "invoices",
      invoiceId,
      "pay",
      "regenerate"
    ),
    options: {
      onMutate: () => {
        setOpening(true)
        winRef.current = window.open("", "_blank")
      },
      onSuccess: (res) => {
        const url = res.data?.url
        if (url && winRef.current && !winRef.current.closed) {
          winRef.current.location.href = url
        } else {
          winRef.current?.close()
          if (url) window.location.href = url
        }
        winRef.current = null
        setOpening(false)
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.invoices(organizationId),
        })
      },
      onError: () => {
        winRef.current?.close()
        winRef.current = null
        toast.error(t("billing.invoices.regenerateFailed"))
        setOpening(false)
      },
    },
  })

  return (
    <Button
      size="sm"
      variant="outline"
      disabled={opening}
      onClick={() => mutate()}
    >
      {opening ? (
        <IconLoader2 data-icon="inline-start" className="animate-spin" />
      ) : (
        <IconRefresh data-icon="inline-start" />
      )}
      {opening
        ? t("billing.invoices.regenerating")
        : t("billing.invoices.regenerate")}
    </Button>
  )
}
