import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 text-center">
      <p className="text-6xl font-extrabold text-muted-foreground">404</p>
      <h1 className="text-xl font-bold">{t("errors.notFoundTitle")}</h1>
      <p className="max-w-xs text-sm text-muted-foreground">
        {t("errors.notFoundDescription")}
      </p>
      <Button nativeButton={false} render={<Link to="/organizations" />}>
        {t("errors.notFoundAction")}
      </Button>
    </div>
  )
}
