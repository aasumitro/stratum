import { IconAlertTriangle } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"

interface ErrorStateProps {
  message?: string
  onRetry?: () => void
}

export function ErrorState({ message, onRetry }: ErrorStateProps) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <div className="flex size-14 items-center justify-center rounded-2xl bg-destructive/10">
        <IconAlertTriangle className="size-7 text-destructive" />
      </div>
      <p className="max-w-xs text-sm text-muted-foreground">
        {message ?? t("errors.stateDefaultMessage")}
      </p>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          {t("errors.tryAgain")}
        </Button>
      )}
    </div>
  )
}
