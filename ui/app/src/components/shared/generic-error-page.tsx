import { useTranslation } from "react-i18next"
import { IconAlertTriangle } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"

export function GenericErrorPage() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 text-center">
      <div className="flex size-14 items-center justify-center rounded-2xl bg-destructive/10">
        <IconAlertTriangle className="size-7 text-destructive" />
      </div>
      <h1 className="text-xl font-bold">{t("errors.genericTitle")}</h1>
      <p className="max-w-xs text-sm text-muted-foreground">
        {t("errors.genericDescription")}
      </p>
      <Button onClick={() => window.location.reload()}>
        {t("errors.tryAgain")}
      </Button>
    </div>
  )
}
